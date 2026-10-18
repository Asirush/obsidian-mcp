package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVaultSecurityAndCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	v, err := New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create vault: %v", err)
	}

	// 1. Path traversal must be rejected
	if _, _, err := v.ResolveSafePath("../../etc/passwd"); !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("expected ErrPathTraversal, got %v", err)
	}

	// 2. Create note with frontmatter
	fm := map[string]any{
		"tags":   []string{"project/mcp", "golang"},
		"status": "in-progress",
	}
	summary, err := v.CreateNote("Projects/Obsidian MCP", "# Obsidian MCP\n\nBuilding with [[Architecture]] and #ai/agents.\n\n```go\n// #not-a-tag [[NotALink]]\n```\n", fm, false)
	if err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}
	if summary.Path != "Projects/Obsidian MCP.md" {
		t.Fatalf("expected path 'Projects/Obsidian MCP.md', got %q", summary.Path)
	}

	// 3. Read note by wikilink name ("Obsidian MCP")
	detail, err := v.ReadNote("[[Obsidian MCP]]")
	if err != nil {
		t.Fatalf("ReadNote by wikilink failed: %v", err)
	}
	if detail.Frontmatter["status"] != "in-progress" {
		t.Fatalf("expected frontmatter status=in-progress, got %v", detail.Frontmatter["status"])
	}
	// Check tags (should include project/mcp, golang, ai/agents, and NOT #not-a-tag inside code block)
	tagSet := make(map[string]bool)
	for _, tg := range detail.Tags {
		tagSet[tg] = true
	}
	if !tagSet["project/mcp"] || !tagSet["golang"] || !tagSet["ai/agents"] {
		t.Fatalf("missing expected tags in %v", detail.Tags)
	}
	if tagSet["not-a-tag"] {
		t.Fatalf("code block tag #not-a-tag should have been ignored, got %v", detail.Tags)
	}

	// 4. Append under heading
	_, err = v.AppendNote("Obsidian MCP", "## Tasks\n- [ ] Write unit tests", "", false)
	if err != nil {
		t.Fatalf("AppendNote failed: %v", err)
	}
	_, err = v.AppendNote("Obsidian MCP", "- [ ] Add daily notes", "Tasks", false)
	if err != nil {
		t.Fatalf("AppendNote under heading failed: %v", err)
	}
	detail, _ = v.ReadNote("Obsidian MCP")
	if !strings.Contains(detail.Body, "- [ ] Write unit tests\n- [ ] Add daily notes") {
		t.Fatalf("expected item appended under ## Tasks, got:\n%s", detail.Body)
	}

	// 5. Patch note
	_, count, err := v.PatchNote("Obsidian MCP", "- [ ] Write unit tests", "- [x] Write unit tests", false, false)
	if err != nil || count != 1 {
		t.Fatalf("PatchNote failed: count=%d, err=%v", count, err)
	}

	// 6. Update frontmatter
	_, updatedFM, err := v.UpdateFrontmatter("Obsidian MCP", map[string]any{"status": "completed", "priority": 1}, nil)
	if err != nil {
		t.Fatalf("UpdateFrontmatter failed: %v", err)
	}
	if updatedFM["status"] != "completed" {
		t.Fatalf("expected status=completed, got %v", updatedFM["status"])
	}
}

func TestLinksBacklinksAndMove(t *testing.T) {
	tmpDir := t.TempDir()
	v, _ := New(tmpDir)

	_, _ = v.CreateNote("Architecture.md", "# Architecture\nDetails here.", nil, false)
	_, _ = v.CreateNote("Index.md", "See [[Architecture|System Arch]] and [[MissingNote]].", nil, false)

	// Outgoing links from Index.md
	_, outLinks, err := v.GetOutgoingLinks("Index")
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}
	if len(outLinks) != 2 {
		t.Fatalf("expected 2 outgoing links, got %d", len(outLinks))
	}
	if !outLinks[0].Exists || outLinks[0].ResolvedPath != "Architecture.md" {
		t.Fatalf("expected Architecture link to resolve, got %+v", outLinks[0])
	}
	if outLinks[1].Exists {
		t.Fatalf("expected MissingNote link to be unresolved, got %+v", outLinks[1])
	}

	// Backlinks to Architecture.md
	_, backlinks, err := v.GetBacklinks("Architecture")
	if err != nil {
		t.Fatalf("GetBacklinks failed: %v", err)
	}
	if len(backlinks) != 1 || backlinks[0].SourcePath != "Index.md" {
		t.Fatalf("expected 1 backlink from Index.md, got %+v", backlinks)
	}

	// Broken links
	broken, err := v.GetBrokenLinks("")
	if err != nil || len(broken) != 1 || broken[0].Target != "MissingNote" {
		t.Fatalf("expected 1 broken link to MissingNote, got %+v (err=%v)", broken, err)
	}

	// Move Architecture.md -> Design/SystemDesign.md and verify wikilink in Index.md is updated!
	moveRes, err := v.MoveNote("Architecture.md", "Design/SystemDesign.md", false, true)
	if err != nil {
		t.Fatalf("MoveNote failed: %v", err)
	}
	if moveRes.UpdatedLinkedFiles != 1 {
		t.Fatalf("expected 1 linked file updated, got %d", moveRes.UpdatedLinkedFiles)
	}
	indexNote, _ := v.ReadNote("Index.md")
	if !strings.Contains(indexNote.Content, "[[SystemDesign|System Arch]]") {
		t.Fatalf("expected wikilink to be rewritten to [[SystemDesign|System Arch]], got: %s", indexNote.Content)
	}

	// Safe delete to .trash
	_, dest, err := v.DeleteNote("Design/SystemDesign.md", false)
	if err != nil {
		t.Fatalf("DeleteNote failed: %v", err)
	}
	if dest != ".trash/Design/SystemDesign.md" {
		t.Fatalf("expected trash destination '.trash/Design/SystemDesign.md', got %q", dest)
	}
}

func TestDailyNotesWithConfigAndTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	v, _ := New(tmpDir)

	obsidianDir := filepath.Join(tmpDir, ".obsidian")
	_ = os.MkdirAll(obsidianDir, 0755)
	_ = os.WriteFile(filepath.Join(obsidianDir, "daily-notes.json"), []byte(`{
		"folder": "Journal/Daily",
		"format": "YYYY-MM-DD",
		"template": "Templates/Daily Template"
	}`), 0644)

	_, _ = v.CreateNote("Templates/Daily Template.md", "# Daily Note {{date}}\n\n## Tasks\n\n## Notes\n", nil, false)

	detail, created, err := v.GetOrCreateDailyNote("2026-10-18", true, "")
	if err != nil {
		t.Fatalf("GetOrCreateDailyNote failed: %v", err)
	}
	if !created {
		t.Fatalf("expected daily note to be newly created")
	}
	if detail.Path != "Journal/Daily/2026-10-18.md" {
		t.Fatalf("expected path 'Journal/Daily/2026-10-18.md', got %q", detail.Path)
	}
	if !strings.Contains(detail.Content, "# Daily Note 2026-10-18") {
		t.Fatalf("expected template variable {{date}} to be expanded, got:\n%s", detail.Content)
	}

	_, err = v.AppendToDailyNote("2026-10-18", "- [ ] Ship Obsidian MCP in Go", "Tasks", "")
	if err != nil {
		t.Fatalf("AppendToDailyNote failed: %v", err)
	}
	updated, _ := v.ReadNote("Journal/Daily/2026-10-18.md")
	if !strings.Contains(updated.Content, "## Tasks\n- [ ] Ship Obsidian MCP in Go\n\n## Notes") {
		t.Fatalf("expected task inserted under ## Tasks before ## Notes, got:\n%s", updated.Content)
	}
}

