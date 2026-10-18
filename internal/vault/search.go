package vault

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// EntryInfo represents a note or directory entry in the vault.
type EntryInfo struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

// ListNotes returns notes and folders within the specified vault directory.
func (v *Vault) ListNotes(folder string, recursive bool) ([]EntryInfo, error) {
	startAbs, startRel, err := v.ResolveSafePath(folder)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(startAbs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path %q is not a directory", folder)
	}

	var entries []EntryInfo

	if !recursive {
		dirEntries, err := os.ReadDir(startAbs)
		if err != nil {
			return nil, err
		}
		for _, d := range dirEntries {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			rel := name
			if startRel != "" {
				rel = startRel + "/" + name
			}
			if d.IsDir() {
				entries = append(entries, EntryInfo{
					Path:  rel,
					Name:  name,
					IsDir: true,
				})
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".md" && ext != ".markdown" && ext != ".canvas" {
				continue
			}
			fInfo, err := d.Info()
			if err != nil {
				continue
			}
			entries = append(entries, EntryInfo{
				Path:       rel,
				Name:       NoteName(name),
				IsDir:      false,
				Size:       fInfo.Size(),
				ModifiedAt: fInfo.ModTime().Format(time.RFC3339),
			})
		}
	} else {
		err = filepath.WalkDir(startAbs, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || path == startAbs {
				return nil
			}
			name := d.Name()
			if d.IsDir() {
				if IsHiddenOrSystemDir(name) {
					return filepath.SkipDir
				}
				rel, err := filepath.Rel(v.root, path)
				if err == nil {
					entries = append(entries, EntryInfo{
						Path:  filepath.ToSlash(rel),
						Name:  name,
						IsDir: true,
					})
				}
				return nil
			}
			if strings.HasPrefix(name, ".") {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".md" && ext != ".markdown" && ext != ".canvas" {
				return nil
			}
			fInfo, err := d.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(v.root, path)
			if err != nil {
				return nil
			}
			entries = append(entries, EntryInfo{
				Path:       filepath.ToSlash(rel),
				Name:       NoteName(name),
				IsDir:      false,
				Size:       fInfo.Size(),
				ModifiedAt: fInfo.ModTime().Format(time.RFC3339),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir // Directories first
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

// SearchOptions configures a full-text or regex search across the vault.
type SearchOptions struct {
	Query         string
	UseRegex      bool
	CaseSensitive bool
	Folder        string
	Tag           string
	ContextLines  int
	Limit         int
}

// LineMatch represents a single matching line with surrounding context.
type LineMatch struct {
	Line    int      `json:"line"`
	Text    string   `json:"text"`
	Context []string `json:"context,omitempty"`
}

// SearchResult represents a note that matched a search query.
type SearchResult struct {
	Path       string      `json:"path"`
	Name       string      `json:"name"`
	MatchCount int         `json:"match_count"`
	Matches    []LineMatch `json:"matches"`
}

// SearchNotes performs full-text or regex search across notes in the vault.
func (v *Vault) SearchNotes(opts SearchOptions) ([]SearchResult, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}
	if opts.ContextLines < 0 {
		opts.ContextLines = 0
	}
	if opts.ContextLines > 10 {
		opts.ContextLines = 10
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	var re *regexp.Regexp
	var err error
	if opts.UseRegex {
		pattern := opts.Query
		if !opts.CaseSensitive && !strings.HasPrefix(pattern, "(?i)") {
			pattern = "(?i)" + pattern
		}
		re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", opts.Query, err)
		}
	}

	queryCompare := opts.Query
	if !opts.CaseSensitive {
		queryCompare = strings.ToLower(opts.Query)
	}
	filterTag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(opts.Tag), "#"))

	var results []SearchResult
	err = v.WalkNotes(opts.Folder, func(relPath string, absPath string, _ fs.FileInfo) error {
		if len(results) >= opts.Limit {
			return filepath.SkipAll
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		raw := strings.ReplaceAll(string(data), "\r\n", "\n")

		if filterTag != "" {
			fm, body, _ := ParseFrontmatter(raw)
			noteTags := ExtractTags(fm, body)
			hasTag := false
			for _, nt := range noteTags {
				ntLower := strings.ToLower(nt)
				if ntLower == filterTag || strings.HasPrefix(ntLower, filterTag+"/") {
					hasTag = true
					break
				}
			}
			if !hasTag {
				return nil
			}
		}

		lines := strings.Split(raw, "\n")
		var lineMatches []LineMatch

		for i, line := range lines {
			matched := false
			if opts.UseRegex {
				matched = re.MatchString(line)
			} else if opts.CaseSensitive {
				matched = strings.Contains(line, queryCompare)
			} else {
				matched = strings.Contains(strings.ToLower(line), queryCompare)
			}

			if matched {
				lm := LineMatch{
					Line: i + 1,
					Text: line,
				}
				if opts.ContextLines > 0 {
					start := i - opts.ContextLines
					if start < 0 {
						start = 0
					}
					end := i + opts.ContextLines
					if end >= len(lines) {
						end = len(lines) - 1
					}
					ctxSlice := make([]string, 0, end-start+1)
					for c := start; c <= end; c++ {
						ctxSlice = append(ctxSlice, fmt.Sprintf("%d: %s", c+1, lines[c]))
					}
					lm.Context = ctxSlice
				}
				lineMatches = append(lineMatches, lm)
			}
		}

		if len(lineMatches) > 0 {
			results = append(results, SearchResult{
				Path:       relPath,
				Name:       NoteName(relPath),
				MatchCount: len(lineMatches),
				Matches:    lineMatches,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return results, nil
}

// FindNotesByName finds notes whose filename or relative path matches the query (substring or glob).
func (v *Vault) FindNotesByName(query string, folder string, limit int) ([]EntryInfo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}
	if limit <= 0 {
		limit = 50
	}

	queryLower := strings.ToLower(query)
	isGlob := strings.ContainsAny(queryLower, "*?[")

	var matches []EntryInfo
	err := v.WalkNotes(folder, func(relPath string, _ string, info fs.FileInfo) error {
		if len(matches) >= limit {
			return filepath.SkipAll
		}
		relLower := strings.ToLower(relPath)
		baseLower := strings.ToLower(filepath.Base(relPath))
		nameLower := strings.ToLower(NoteName(relPath))

		matched := false
		if isGlob {
			if ok, _ := filepath.Match(queryLower, baseLower); ok {
				matched = true
			} else if ok, _ := filepath.Match(queryLower, nameLower); ok {
				matched = true
			} else if ok, _ := filepath.Match(queryLower, relLower); ok {
				matched = true
			}
		} else {
			matched = strings.Contains(nameLower, queryLower) || strings.Contains(relLower, queryLower)
		}

		if matched {
			matches = append(matches, EntryInfo{
				Path:       relPath,
				Name:       NoteName(relPath),
				IsDir:      false,
				Size:       info.Size(),
				ModifiedAt: info.ModTime().Format(time.RFC3339),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// VaultStats holds summary statistics for the entire vault.
type VaultStats struct {
	VaultRoot        string      `json:"vault_root"`
	TotalNotes       int         `json:"total_notes"`
	TotalFolders     int         `json:"total_folders"`
	TotalSizeBytes   int64       `json:"total_size_bytes"`
	TotalUniqueTags  int         `json:"total_unique_tags"`
	TotalLinks       int         `json:"total_links"`
	BrokenLinksCount int         `json:"broken_links_count"`
	OrphanNotesCount int         `json:"orphan_notes_count"`
	RecentNotes      []EntryInfo `json:"recent_notes"`
}

// GetVaultStats computes an overview of the vault's notes, tags, links, and recent activity.
func (v *Vault) GetVaultStats() (*VaultStats, error) {
	entries, err := v.ListNotes("", true)
	if err != nil {
		return nil, err
	}

	stats := &VaultStats{
		VaultRoot: v.root,
	}

	var noteEntries []EntryInfo
	for _, e := range entries {
		if e.IsDir {
			stats.TotalFolders++
		} else {
			stats.TotalNotes++
			stats.TotalSizeBytes += e.Size
			noteEntries = append(noteEntries, e)
		}
	}

	if tags, err := v.ListTags("", "", false); err == nil {
		stats.TotalUniqueTags = len(tags)
	}

	index, _, _ := v.buildNoteIndex()
	incoming := make(map[string]int)

	_ = v.WalkNotes("", func(relPath string, absPath string, _ fs.FileInfo) error {
		if _, ok := incoming[relPath]; !ok {
			incoming[relPath] = 0
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		links := ExtractLinks(string(data))
		stats.TotalLinks += len(links)
		for _, l := range links {
			resolved, exists := v.resolveLinkTarget(relPath, l.Target, index)
			if !exists {
				stats.BrokenLinksCount++
			} else if resolved != relPath {
				incoming[resolved]++
			}
		}
		return nil
	})

	for _, count := range incoming {
		if count == 0 {
			stats.OrphanNotesCount++
		}
	}

	sort.Slice(noteEntries, func(i, j int) bool {
		return noteEntries[i].ModifiedAt > noteEntries[j].ModifiedAt
	})
	if len(noteEntries) > 10 {
		noteEntries = noteEntries[:10]
	}
	stats.RecentNotes = noteEntries

	return stats, nil
}

