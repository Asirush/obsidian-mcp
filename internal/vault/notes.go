package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// NoteDetail holds full details of a read note.
type NoteDetail struct {
	Path          string         `json:"path"`
	Name          string         `json:"name"`
	Content       string         `json:"content"`
	Body          string         `json:"body"`
	Frontmatter   map[string]any `json:"frontmatter"`
	Tags          []string       `json:"tags"`
	OutgoingLinks []Link         `json:"outgoing_links"`
	Size          int64          `json:"size"`
	ModifiedAt    string         `json:"modified_at"`
}

// NoteSummary is returned after write/modify operations.
type NoteSummary struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// MoveResult summarizes a move/rename operation.
type MoveResult struct {
	OldPath            string `json:"old_path"`
	NewPath            string `json:"new_path"`
	UpdatedLinkedFiles int    `json:"updated_linked_files"`
}

// ReadNote reads a note by path or wikilink name and parses its metadata.
func (v *Vault) ReadNote(identifier string) (*NoteDetail, error) {
	abs, rel, err := v.ResolveNote(identifier, true)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	raw := string(data)
	fm, body, fmErr := ParseFrontmatter(raw)
	if fmErr != nil {
		fm = map[string]any{}
		body = raw
	}
	tags := ExtractTags(fm, body)
	links := ExtractLinks(raw)
	if index, _, idxErr := v.buildNoteIndex(); idxErr == nil {
		for i := range links {
			resolved, exists := v.resolveLinkTarget(rel, links[i].Target, index)
			links[i].ResolvedPath = resolved
			links[i].Exists = exists
		}
	}

	return &NoteDetail{
		Path:          rel,
		Name:          NoteName(rel),
		Content:       raw,
		Body:          body,
		Frontmatter:   fm,
		Tags:          tags,
		OutgoingLinks: links,
		Size:          info.Size(),
		ModifiedAt:    info.ModTime().Format(time.RFC3339),
	}, nil
}

// CreateNote creates a new markdown note in the vault.
func (v *Vault) CreateNote(relPath string, content string, frontmatter map[string]any, overwrite bool) (*NoteSummary, error) {
	abs, rel, err := v.ResolveNote(relPath, false)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(abs); statErr == nil && !overwrite {
		return nil, fmt.Errorf("%w: %s (set overwrite=true to replace)", ErrNoteExists, rel)
	}

	finalContent := content
	if len(frontmatter) > 0 {
		existingFM, body, err := ParseFrontmatter(content)
		if err == nil && len(existingFM) > 0 {
			for k, val := range frontmatter {
				existingFM[k] = val
			}
			finalContent, err = ComposeNote(existingFM, body)
			if err != nil {
				return nil, err
			}
		} else {
			finalContent, err = ComposeNote(frontmatter, content)
			if err != nil {
				return nil, err
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return nil, fmt.Errorf("failed to create parent directory: %w", err)
	}
	if err := os.WriteFile(abs, []byte(finalContent), 0644); err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	return &NoteSummary{
		Path:       rel,
		Name:       NoteName(rel),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().Format(time.RFC3339),
	}, nil
}

// UpdateNote updates an existing note's content (or creates it if createIfMissing is true).
// If preserveFrontmatter is true and the new content does not include YAML frontmatter,
// the existing note's frontmatter is retained.
func (v *Vault) UpdateNote(identifier string, content string, frontmatter map[string]any, preserveFrontmatter bool, createIfMissing bool) (*NoteSummary, error) {
	abs, rel, err := v.ResolveNote(identifier, true)
	if err != nil {
		if errors.Is(err, ErrNoteNotFound) && createIfMissing {
			return v.CreateNote(identifier, content, frontmatter, true)
		}
		return nil, err
	}

	var mergedFM map[string]any
	if preserveFrontmatter {
		if existingBytes, readErr := os.ReadFile(abs); readErr == nil {
			if oldFM, _, fmErr := ParseFrontmatter(string(existingBytes)); fmErr == nil && len(oldFM) > 0 {
				mergedFM = oldFM
			}
		}
	}

	newFM, body, fmErr := ParseFrontmatter(content)
	if fmErr == nil && len(newFM) > 0 {
		if mergedFM == nil {
			mergedFM = make(map[string]any)
		}
		for k, val := range newFM {
			mergedFM[k] = val
		}
	} else {
		body = content
	}

	for k, val := range frontmatter {
		if mergedFM == nil {
			mergedFM = make(map[string]any)
		}
		mergedFM[k] = val
	}

	finalContent := content
	if len(mergedFM) > 0 {
		finalContent, err = ComposeNote(mergedFM, body)
		if err != nil {
			return nil, err
		}
	}

	if err := os.WriteFile(abs, []byte(finalContent), 0644); err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	return &NoteSummary{
		Path:       rel,
		Name:       NoteName(rel),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().Format(time.RFC3339),
	}, nil
}

// AppendNote appends text to the end of a note or under a specific markdown heading.
func (v *Vault) AppendNote(identifier string, text string, heading string, createIfMissing bool) (*NoteSummary, error) {
	abs, rel, err := v.ResolveNote(identifier, true)
	if err != nil {
		if errors.Is(err, ErrNoteNotFound) && createIfMissing {
			initial := text
			if strings.TrimSpace(heading) != "" {
				hdr := normalizeHeading(heading)
				initial = hdr + "\n" + text
			}
			return v.CreateNote(identifier, initial, nil, true)
		}
		return nil, err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	existing := strings.ReplaceAll(string(data), "\r\n", "\n")

	var updated string
	if strings.TrimSpace(heading) == "" {
		if existing == "" {
			updated = text
		} else if strings.HasSuffix(existing, "\n") {
			updated = existing + text
		} else {
			updated = existing + "\n" + text
		}
	} else {
		updated = appendUnderHeading(existing, heading, text)
	}

	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}

	if err := os.WriteFile(abs, []byte(updated), 0644); err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	return &NoteSummary{
		Path:       rel,
		Name:       NoteName(rel),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().Format(time.RFC3339),
	}, nil
}

func normalizeHeading(h string) string {
	h = strings.TrimSpace(h)
	if strings.HasPrefix(h, "#") {
		return h
	}
	return "## " + h
}

func parseHeadingLine(line string) (level int, title string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	lvl := 0
	for lvl < len(trimmed) && trimmed[lvl] == '#' {
		lvl++
	}
	if lvl > 6 || lvl == len(trimmed) || (trimmed[lvl] != ' ' && trimmed[lvl] != '\t') {
		return 0, "", false
	}
	return lvl, strings.TrimSpace(trimmed[lvl:]), true
}

func appendUnderHeading(doc string, targetHeading string, contentToAdd string) string {
	targetTitle := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(targetHeading), "#"))
	lines := strings.Split(doc, "\n")

	foundIdx := -1
	foundLevel := 0
	inFence := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lvl, title, ok := parseHeadingLine(line)
		if !ok {
			continue
		}
		if foundIdx == -1 {
			if strings.EqualFold(title, targetTitle) || strings.EqualFold(trimmed, strings.TrimSpace(targetHeading)) {
				foundIdx = i
				foundLevel = lvl
			}
		} else if lvl <= foundLevel {
			// Insert right before this next section heading
			insertPos := i
			for insertPos > foundIdx+1 && strings.TrimSpace(lines[insertPos-1]) == "" {
				insertPos--
			}
			before := strings.Join(lines[:insertPos], "\n")
			after := strings.Join(lines[i:], "\n")
			return before + "\n" + contentToAdd + "\n\n" + after
		}
	}

	if foundIdx != -1 {
		// Target heading was the last section in the file
		trimmedDoc := strings.TrimRight(doc, "\n")
		return trimmedDoc + "\n" + contentToAdd + "\n"
	}

	// Heading not found; append heading + content at the end of the file
	hdr := normalizeHeading(targetHeading)
	trimmedDoc := strings.TrimRight(doc, "\n")
	if trimmedDoc == "" {
		return hdr + "\n" + contentToAdd + "\n"
	}
	return trimmedDoc + "\n\n" + hdr + "\n" + contentToAdd + "\n"
}

// PatchNote performs an exact or regex search-and-replace within a note.
func (v *Vault) PatchNote(identifier string, search string, replace string, useRegex bool, replaceAll bool) (string, int, error) {
	if search == "" {
		return "", 0, errors.New("search pattern cannot be empty")
	}
	abs, rel, err := v.ResolveNote(identifier, true)
	if err != nil {
		return "", 0, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", 0, err
	}
	original := string(data)
	var updated string
	count := 0

	if useRegex {
		re, err := regexp.Compile(search)
		if err != nil {
			return rel, 0, fmt.Errorf("invalid regex %q: %w", search, err)
		}
		matches := re.FindAllStringIndex(original, -1)
		if len(matches) == 0 {
			return rel, 0, fmt.Errorf("pattern %q not found in %s", search, rel)
		}
		if replaceAll {
			count = len(matches)
			updated = re.ReplaceAllString(original, replace)
		} else {
			count = 1
			first := matches[0]
			replacedChunk := re.ReplaceAllString(original[first[0]:first[1]], replace)
			updated = original[:first[0]] + replacedChunk + original[first[1]:]
		}
	} else {
		occurrences := strings.Count(original, search)
		if occurrences == 0 {
			return rel, 0, fmt.Errorf("text %q not found in %s", search, rel)
		}
		if replaceAll {
			count = occurrences
			updated = strings.ReplaceAll(original, search, replace)
		} else {
			count = 1
			updated = strings.Replace(original, search, replace, 1)
		}
	}

	if err := os.WriteFile(abs, []byte(updated), 0644); err != nil {
		return rel, 0, err
	}
	return rel, count, nil
}

// DeleteNote deletes a note or moves it to the Obsidian ".trash" directory inside the vault.
func (v *Vault) DeleteNote(identifier string, permanent bool) (string, string, error) {
	abs, rel, err := v.ResolveNote(identifier, true)
	if err != nil {
		return "", "", err
	}

	if permanent {
		if err := os.Remove(abs); err != nil {
			return rel, "", err
		}
		return rel, "deleted_permanently", nil
	}

	trashRel := filepath.ToSlash(filepath.Join(".trash", rel))
	trashAbs, _, err := v.ResolveSafePath(trashRel)
	if err != nil {
		return rel, "", err
	}
	if err := os.MkdirAll(filepath.Dir(trashAbs), 0755); err != nil {
		return rel, "", err
	}
	if err := os.Rename(abs, trashAbs); err != nil {
		return rel, "", err
	}
	return rel, trashRel, nil
}

// MoveNote moves or renames a note to a new vault-relative path and optionally updates wikilinks across the vault.
func (v *Vault) MoveNote(source string, destination string, overwrite bool, updateLinks bool) (*MoveResult, error) {
	srcAbs, srcRel, err := v.ResolveNote(source, true)
	if err != nil {
		return nil, err
	}
	dstAbs, dstRel, err := v.ResolveNote(destination, false)
	if err != nil {
		return nil, err
	}
	if srcAbs == dstAbs {
		return &MoveResult{OldPath: srcRel, NewPath: dstRel, UpdatedLinkedFiles: 0}, nil
	}

	if _, statErr := os.Stat(dstAbs); statErr == nil && !overwrite {
		return nil, fmt.Errorf("%w: %s (set overwrite=true to replace)", ErrNoteExists, dstRel)
	}

	if err := os.MkdirAll(filepath.Dir(dstAbs), 0755); err != nil {
		return nil, err
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		return nil, err
	}

	updatedCount := 0
	if updateLinks {
		updatedCount, _ = v.RewriteLinksOnMove(srcRel, dstRel)
	}

	return &MoveResult{
		OldPath:            srcRel,
		NewPath:            dstRel,
		UpdatedLinkedFiles: updatedCount,
	}, nil
}
