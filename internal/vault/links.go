package vault

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	// Matches [[target]] or ![[target]]
	wikilinkRe = regexp.MustCompile(`(!?)\[\[([^\[\]\n]+)\]\]`)
	// Matches [text](url) or ![text](url)
	markdownLinkRe = regexp.MustCompile(`(!?)\[([^\[\]\n]*)\]\(([^\(\)\n]+)\)`)
)

// Link represents a parsed outgoing link inside a note.
type Link struct {
	Raw          string `json:"raw"`
	Target       string `json:"target"`
	Subpath      string `json:"subpath,omitempty"` // #Heading or ^block-id
	Alias        string `json:"alias,omitempty"`
	IsEmbed      bool   `json:"is_embed,omitempty"`
	IsWikiLink   bool   `json:"is_wikilink"`
	Line         int    `json:"line,omitempty"`
	ResolvedPath string `json:"resolved_path,omitempty"`
	Exists       bool   `json:"exists"`
}

// Backlink represents an incoming link from another note in the vault.
type Backlink struct {
	SourcePath string `json:"source_path"`
	Line       int    `json:"line"`
	Context    string `json:"context"`
	Link       Link   `json:"link"`
}

// BrokenLinkEntry represents an unresolved link in a specific source note.
type BrokenLinkEntry struct {
	SourcePath string `json:"source_path"`
	Line       int    `json:"line"`
	Raw        string `json:"raw"`
	Target     string `json:"target"`
}

// OrphanNote represents a note with no incoming backlinks.
type OrphanNote struct {
	Path              string `json:"path"`
	OutgoingLinkCount int    `json:"outgoing_link_count"`
}

// ExtractLinks parses all wikilinks and internal markdown links from markdown text,
// ignoring fenced code blocks and inline code.
func ExtractLinks(raw string) []Link {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	inFence := false
	fenceMarker := ""
	inFrontmatter := false

	var links []Link

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1
		trimmed := strings.TrimSpace(line)

		// Skip YAML frontmatter block at start of file
		if lineIdx == 0 && trimmed == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if trimmed == "---" || trimmed == "..." {
				inFrontmatter = false
			}
			continue
		}

		// Track fenced code blocks
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inFence {
				inFence = true
				fenceMarker = marker
				continue
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
				continue
			}
		}
		if inFence {
			continue
		}

		// Mask inline code spans so links inside `...` are ignored
		cleanedLine := inlineCodeRe.ReplaceAllStringFunc(line, func(s string) string {
			return strings.Repeat(" ", len(s))
		})

		// 1. Parse [[wikilinks]]
		wikiMatches := wikilinkRe.FindAllStringSubmatch(cleanedLine, -1)
		for _, m := range wikiMatches {
			isEmbed := m[1] == "!"
			inner := m[2]
			alias := ""
			targetAndSub := inner
			if pipeIdx := strings.Index(inner, "|"); pipeIdx != -1 {
				targetAndSub = inner[:pipeIdx]
				alias = strings.TrimSpace(inner[pipeIdx+1:])
			}
			subpath := ""
			target := targetAndSub
			if hashIdx := strings.Index(targetAndSub, "#"); hashIdx != -1 {
				target = targetAndSub[:hashIdx]
				subpath = targetAndSub[hashIdx:]
			}
			target = strings.TrimSpace(target)
			if target == "" {
				// Same-note heading link like [[#Heading]]; skip as external note link
				continue
			}
			links = append(links, Link{
				Raw:        m[0],
				Target:     target,
				Subpath:    subpath,
				Alias:      alias,
				IsEmbed:    isEmbed,
				IsWikiLink: true,
				Line:       lineNum,
			})
		}

		// 2. Parse [text](url) internal markdown links
		mdMatches := markdownLinkRe.FindAllStringSubmatch(cleanedLine, -1)
		for _, m := range mdMatches {
			isEmbed := m[1] == "!"
			alias := m[2]
			rawTarget := strings.TrimSpace(m[3])
			// Strip optional title in markdown link: [text](path "title")
			if spaceIdx := strings.IndexAny(rawTarget, " \t"); spaceIdx != -1 && !strings.HasPrefix(rawTarget, "<") {
				rawTarget = rawTarget[:spaceIdx]
			}
			rawTarget = strings.TrimPrefix(strings.TrimSuffix(rawTarget, ">"), "<")
			if isExternalTarget(rawTarget) || strings.HasPrefix(rawTarget, "#") {
				continue
			}
			decoded, err := url.PathUnescape(rawTarget)
			if err == nil {
				rawTarget = decoded
			}
			subpath := ""
			target := rawTarget
			if hashIdx := strings.Index(rawTarget, "#"); hashIdx != -1 {
				target = rawTarget[:hashIdx]
				subpath = rawTarget[hashIdx:]
			}
			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}
			links = append(links, Link{
				Raw:        m[0],
				Target:     target,
				Subpath:    subpath,
				Alias:      alias,
				IsEmbed:    isEmbed,
				IsWikiLink: false,
				Line:       lineNum,
			})
		}
	}

	return links
}

func isExternalTarget(target string) bool {
	lower := strings.ToLower(target)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "obsidian://") ||
		strings.HasPrefix(lower, "data:")
}

// buildNoteIndex builds a fast lookup index of all notes in the vault.
// Maps lowercase relative path (with and without .md) and lowercase basename to canonical relative path.
func (v *Vault) buildNoteIndex() (map[string]string, []string, error) {
	index := make(map[string]string)
	var allNotes []string

	err := v.WalkNotes("", func(relPath string, _ string, _ fs.FileInfo) error {
		allNotes = append(allNotes, relPath)
		lowerRel := strings.ToLower(relPath)
		index[lowerRel] = relPath

		noExt := strings.TrimSuffix(lowerRel, filepath.Ext(lowerRel))
		index[noExt] = relPath

		baseName := strings.ToLower(NoteName(relPath))
		if _, exists := index[baseName]; !exists {
			index[baseName] = relPath
		}
		return nil
	})
	return index, allNotes, err
}

func (v *Vault) resolveLinkTarget(sourceRelPath string, target string, index map[string]string) (string, bool) {
	cleaned := filepath.ToSlash(strings.TrimSpace(target))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return "", false
	}

	// 1. Check relative to source note's directory if ./ or ../
	if strings.HasPrefix(cleaned, "./") || strings.HasPrefix(cleaned, "../") {
		joined := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(sourceRelPath), cleaned)))
		lowerJoined := strings.ToLower(joined)
		if resolved, ok := index[lowerJoined]; ok {
			return resolved, true
		}
	}

	// 2. Check direct index match (handles full path, path without .md, or basename)
	lowerTarget := strings.ToLower(cleaned)
	if resolved, ok := index[lowerTarget]; ok {
		return resolved, true
	}
	lowerNoExt := strings.TrimSuffix(lowerTarget, ".md")
	if resolved, ok := index[lowerNoExt]; ok {
		return resolved, true
	}

	// 3. Check if it's a non-markdown attachment file in the vault (e.g. image/pdf)
	if abs, rel, err := v.ResolveSafePath(cleaned); err == nil {
		if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
			return rel, true
		}
	}

	return "", false
}

// GetOutgoingLinks returns all outgoing links in a note and whether their targets exist in the vault.
func (v *Vault) GetOutgoingLinks(identifier string) (string, []Link, error) {
	abs, rel, err := v.ResolveNote(identifier, true)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", nil, err
	}

	index, _, err := v.buildNoteIndex()
	if err != nil {
		return "", nil, err
	}

	links := ExtractLinks(string(data))
	for i := range links {
		resolved, exists := v.resolveLinkTarget(rel, links[i].Target, index)
		links[i].ResolvedPath = resolved
		links[i].Exists = exists
	}
	return rel, links, nil
}

// GetBacklinks finds all notes in the vault that link to the specified note.
func (v *Vault) GetBacklinks(identifier string) (string, []Backlink, error) {
	_, targetRel, err := v.ResolveNote(identifier, true)
	if err != nil {
		return "", nil, err
	}

	index, _, err := v.buildNoteIndex()
	if err != nil {
		return "", nil, err
	}

	var backlinks []Backlink
	err = v.WalkNotes("", func(relPath string, absPath string, _ fs.FileInfo) error {
		if relPath == targetRel {
			return nil
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		content := string(data)
		lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
		links := ExtractLinks(content)
		for _, link := range links {
			resolved, exists := v.resolveLinkTarget(relPath, link.Target, index)
			if exists && strings.EqualFold(resolved, targetRel) {
				link.ResolvedPath = resolved
				link.Exists = true
				ctxLine := ""
				if link.Line >= 1 && link.Line <= len(lines) {
					ctxLine = strings.TrimSpace(lines[link.Line-1])
				}
				backlinks = append(backlinks, Backlink{
					SourcePath: relPath,
					Line:       link.Line,
					Context:    ctxLine,
					Link:       link,
				})
			}
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}

	sort.Slice(backlinks, func(i, j int) bool {
		if backlinks[i].SourcePath != backlinks[j].SourcePath {
			return backlinks[i].SourcePath < backlinks[j].SourcePath
		}
		return backlinks[i].Line < backlinks[j].Line
	})
	return targetRel, backlinks, nil
}

// GetBrokenLinks scans the vault (or a folder) for all unresolved outgoing links.
func (v *Vault) GetBrokenLinks(folder string) ([]BrokenLinkEntry, error) {
	index, _, err := v.buildNoteIndex()
	if err != nil {
		return nil, err
	}

	var broken []BrokenLinkEntry
	err = v.WalkNotes(folder, func(relPath string, absPath string, _ fs.FileInfo) error {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		links := ExtractLinks(string(data))
		for _, link := range links {
			_, exists := v.resolveLinkTarget(relPath, link.Target, index)
			if !exists {
				broken = append(broken, BrokenLinkEntry{
					SourcePath: relPath,
					Line:       link.Line,
					Raw:        link.Raw,
					Target:     link.Target,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return broken, nil
}

// GetOrphanNotes returns all notes that have zero incoming backlinks from other notes.
func (v *Vault) GetOrphanNotes(folder string) ([]OrphanNote, error) {
	index, _, err := v.buildNoteIndex()
	if err != nil {
		return nil, err
	}

	incomingCount := make(map[string]int)
	outgoingCount := make(map[string]int)

	err = v.WalkNotes("", func(relPath string, absPath string, _ fs.FileInfo) error {
		if _, ok := incomingCount[relPath]; !ok {
			incomingCount[relPath] = 0
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		links := ExtractLinks(string(data))
		outgoingCount[relPath] = len(links)
		for _, link := range links {
			resolved, exists := v.resolveLinkTarget(relPath, link.Target, index)
			if exists && resolved != relPath {
				incomingCount[resolved]++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	prefix := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(folder)), "/")
	var orphans []OrphanNote
	for notePath, inCount := range incomingCount {
		if prefix != "" && !strings.HasPrefix(notePath, prefix+"/") && notePath != prefix {
			continue
		}
		if inCount == 0 {
			orphans = append(orphans, OrphanNote{
				Path:              notePath,
				OutgoingLinkCount: outgoingCount[notePath],
			})
		}
	}

	sort.Slice(orphans, func(i, j int) bool {
		return orphans[i].Path < orphans[j].Path
	})
	return orphans, nil
}

// RewriteLinksOnMove updates wikilinks across all notes in the vault when a note is renamed or moved.
func (v *Vault) RewriteLinksOnMove(oldRelPath, newRelPath string) (int, error) {
	oldName := NoteName(oldRelPath)
	newName := NoteName(newRelPath)
	oldNoExt := strings.TrimSuffix(oldRelPath, filepath.Ext(oldRelPath))
	newNoExt := strings.TrimSuffix(newRelPath, filepath.Ext(newRelPath))

	updatedFiles := 0
	err := v.WalkNotes("", func(_ string, absPath string, _ fs.FileInfo) error {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		original := string(data)
		modified := wikilinkRe.ReplaceAllStringFunc(original, func(match string) string {
			sub := wikilinkRe.FindStringSubmatch(match)
			if len(sub) < 3 {
				return match
			}
			bang := sub[1]
			inner := sub[2]

			aliasPart := ""
			targetAndSub := inner
			if pipeIdx := strings.Index(inner, "|"); pipeIdx != -1 {
				targetAndSub = inner[:pipeIdx]
				aliasPart = inner[pipeIdx:] // includes "|"
			}
			subpath := ""
			target := targetAndSub
			if hashIdx := strings.Index(targetAndSub, "#"); hashIdx != -1 {
				target = targetAndSub[:hashIdx]
				subpath = targetAndSub[hashIdx:] // includes "#"
			}
			trimmedTarget := strings.TrimSpace(target)
			trimmedNoExt := strings.TrimSuffix(trimmedTarget, ".md")

			if strings.EqualFold(trimmedNoExt, oldNoExt) {
				if oldNoExt != oldName {
					return bang + "[[" + newNoExt + subpath + aliasPart + "]]"
				}
				return bang + "[[" + newName + subpath + aliasPart + "]]"
			}
			if strings.EqualFold(trimmedNoExt, oldName) {
				return bang + "[[" + newName + subpath + aliasPart + "]]"
			}
			return match
		})

		if modified != original {
			if err := os.WriteFile(absPath, []byte(modified), 0644); err == nil {
				updatedFiles++
			}
		}
		return nil
	})
	return updatedFiles, err
}

