package vault

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

var (
	fencedCodeBlockRe = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~")
	inlineCodeRe      = regexp.MustCompile("`[^`\n]+`")
	// Obsidian tag rules: starts with #, followed by allowed tag chars (letters, digits, _, -, /),
	// and must not be purely numeric. Preceded by start of line or whitespace.
	inlineTagRe = regexp.MustCompile(`(?:^|[\s\(\[\{])#([a-zA-Z0-9_\-/]+)`)
)

// SplitFrontmatter splits raw markdown content into raw YAML frontmatter (without --- fences),
// the markdown body, and whether frontmatter was present.
func SplitFrontmatter(raw string) (yamlText string, body string, hasFrontmatter bool) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") && normalized != "---" {
		return "", raw, false
	}

	rest := normalized[4:]
	lines := strings.Split(rest, "\n")
	endIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "---" || trimmed == "..." {
			endIdx = i
			break
		}
	}
	if endIdx == -1 {
		return "", raw, false
	}

	yamlText = strings.Join(lines[:endIdx], "\n")
	body = strings.Join(lines[endIdx+1:], "\n")
	body = strings.TrimPrefix(body, "\n")
	return yamlText, body, true
}

// ParseFrontmatter parses raw markdown content into a frontmatter map and markdown body.
func ParseFrontmatter(raw string) (map[string]any, string, error) {
	yamlText, body, hasFM := SplitFrontmatter(raw)
	if !hasFM || strings.TrimSpace(yamlText) == "" {
		return map[string]any{}, body, nil
	}

	var fm map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &fm); err != nil {
		return nil, body, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	if fm == nil {
		fm = map[string]any{}
	}
	return fm, body, nil
}

// ComposeNote combines YAML frontmatter and markdown body into a complete markdown document.
func ComposeNote(frontmatter map[string]any, body string) (string, error) {
	if len(frontmatter) == 0 {
		return body, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(frontmatter); err != nil {
		return "", fmt.Errorf("failed to encode frontmatter: %w", err)
	}
	_ = enc.Close()

	yamlStr := strings.TrimSpace(buf.String())
	if yamlStr == "" || yamlStr == "{}" {
		return body, nil
	}
	if body != "" && !strings.HasPrefix(body, "\n") {
		return fmt.Sprintf("---\n%s\n---\n\n%s", yamlStr, body), nil
	}
	return fmt.Sprintf("---\n%s\n---\n%s", yamlStr, body), nil
}

// ExtractTags extracts all unique normalized tags (without leading '#') from both YAML frontmatter
// and inline #tags in the markdown body.
func ExtractTags(frontmatter map[string]any, body string) []string {
	seen := make(map[string]string) // lowercase -> original casing
	addTag := func(t string) {
		t = strings.TrimSpace(t)
		t = strings.TrimPrefix(t, "#")
		t = strings.Trim(t, ",")
		if t == "" || isPureNumber(t) {
			return
		}
		lower := strings.ToLower(t)
		if _, exists := seen[lower]; !exists {
			seen[lower] = t
		}
	}

	// 1. Frontmatter tags ("tags" or "tag")
	for _, key := range []string{"tags", "tag"} {
		val, ok := frontmatter[key]
		if !ok || val == nil {
			continue
		}
		switch v := val.(type) {
		case string:
			for _, part := range strings.FieldsFunc(v, func(r rune) bool {
				return r == ',' || unicode.IsSpace(r)
			}) {
				addTag(part)
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					addTag(s)
				} else if item != nil {
					addTag(fmt.Sprintf("%v", item))
				}
			}
		case []string:
			for _, s := range v {
				addTag(s)
			}
		}
	}

	// 2. Inline tags (strip code blocks and inline code first)
	cleanBody := fencedCodeBlockRe.ReplaceAllString(body, "")
	cleanBody = inlineCodeRe.ReplaceAllString(cleanBody, "")
	matches := inlineTagRe.FindAllStringSubmatch(cleanBody, -1)
	for _, m := range matches {
		if len(m) >= 2 {
			addTag(m[1])
		}
	}

	tags := make([]string, 0, len(seen))
	for _, t := range seen {
		tags = append(tags, t)
	}
	sort.Slice(tags, func(i, j int) bool {
		return strings.ToLower(tags[i]) < strings.ToLower(tags[j])
	})
	return tags
}

func isPureNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// GetFrontmatter reads and parses the YAML frontmatter and tags of a note.
func (v *Vault) GetFrontmatter(identifier string) (string, map[string]any, []string, error) {
	abs, rel, err := v.ResolveNote(identifier, true)
	if err != nil {
		return "", nil, nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", nil, nil, err
	}
	fm, body, err := ParseFrontmatter(string(data))
	if err != nil {
		return rel, nil, nil, err
	}
	tags := ExtractTags(fm, body)
	return rel, fm, tags, nil
}

// UpdateFrontmatter merges updates into and/or removes deleteKeys from a note's YAML frontmatter
// while preserving its markdown body.
func (v *Vault) UpdateFrontmatter(identifier string, updates map[string]any, deleteKeys []string) (string, map[string]any, error) {
	abs, rel, err := v.ResolveNote(identifier, true)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", nil, err
	}
	fm, body, err := ParseFrontmatter(string(data))
	if err != nil {
		return rel, nil, err
	}
	for k, val := range updates {
		fm[k] = val
	}
	for _, k := range deleteKeys {
		delete(fm, k)
	}

	composed, err := ComposeNote(fm, body)
	if err != nil {
		return rel, nil, err
	}
	if err := os.WriteFile(abs, []byte(composed), 0644); err != nil {
		return rel, nil, err
	}
	return rel, fm, nil
}

// TagInfo represents aggregated information about a tag across the vault.
type TagInfo struct {
	Tag   string   `json:"tag"`
	Count int      `json:"count"`
	Notes []string `json:"notes,omitempty"`
}

// ListTags scans the vault (or a folder) and returns all tags with their counts and note paths.
func (v *Vault) ListTags(folder string, prefix string, includeNotes bool) ([]TagInfo, error) {
	prefix = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(prefix), "#"))
	tagMap := make(map[string]*TagInfo)

	err := v.WalkNotes(folder, func(relPath string, absPath string, _ fs.FileInfo) error {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		fm, body, err := ParseFrontmatter(string(data))
		if err != nil {
			// Still extract inline tags even if frontmatter has syntax issue
			body = string(data)
			fm = nil
		}
		tags := ExtractTags(fm, body)
		for _, t := range tags {
			lower := strings.ToLower(t)
			if prefix != "" && !strings.HasPrefix(lower, prefix) {
				continue
			}
			entry, exists := tagMap[lower]
			if !exists {
				entry = &TagInfo{Tag: t}
				tagMap[lower] = entry
			}
			entry.Count++
			if includeNotes {
				entry.Notes = append(entry.Notes, relPath)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	results := make([]TagInfo, 0, len(tagMap))
	for _, info := range tagMap {
		sort.Strings(info.Notes)
		results = append(results, *info)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Count != results[j].Count {
			return results[i].Count > results[j].Count
		}
		return strings.ToLower(results[i].Tag) < strings.ToLower(results[j].Tag)
	})
	return results, nil
}

// TagMatchResult represents a note matched by tag search.
type TagMatchResult struct {
	Path        string         `json:"path"`
	Tags        []string       `json:"tags"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
}

// SearchByTag finds notes that have any (or all, if matchAll is true) of the specified tags.
// Supports hierarchical tag matching (e.g., query "project" matches "project" and "project/alpha").
func (v *Vault) SearchByTag(queryTags []string, matchAll bool, folder string) ([]TagMatchResult, error) {
	normalizedQueries := make([]string, 0, len(queryTags))
	for _, qt := range queryTags {
		qt = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(qt), "#"))
		if qt != "" {
			normalizedQueries = append(normalizedQueries, qt)
		}
	}
	if len(normalizedQueries) == 0 {
		return nil, fmt.Errorf("at least one tag must be provided")
	}

	var matches []TagMatchResult
	err := v.WalkNotes(folder, func(relPath string, absPath string, _ fs.FileInfo) error {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		fm, body, err := ParseFrontmatter(string(data))
		if err != nil {
			body = string(data)
			fm = nil
		}
		noteTags := ExtractTags(fm, body)
		if len(noteTags) == 0 {
			return nil
		}

		matchedCount := 0
		for _, q := range normalizedQueries {
			found := false
			for _, nt := range noteTags {
				ntLower := strings.ToLower(nt)
				if ntLower == q || strings.HasPrefix(ntLower, q+"/") {
					found = true
					break
				}
			}
			if found {
				matchedCount++
			}
		}

		if (matchAll && matchedCount == len(normalizedQueries)) || (!matchAll && matchedCount > 0) {
			matches = append(matches, TagMatchResult{
				Path:        relPath,
				Tags:        noteTags,
				Frontmatter: fm,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Path < matches[j].Path
	})
	return matches, nil
}

