package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DailyNotesConfig represents Obsidian's .obsidian/daily-notes.json settings.
type DailyNotesConfig struct {
	Folder   string `json:"folder"`
	Format   string `json:"format"`
	Template string `json:"template"`
}

// LoadDailyNotesConfig reads .obsidian/daily-notes.json if present, or returns defaults.
func (v *Vault) LoadDailyNotesConfig() DailyNotesConfig {
	cfg := DailyNotesConfig{
		Folder: "",
		Format: "YYYY-MM-DD",
	}
	cfgPath := filepath.Join(v.root, ".obsidian", "daily-notes.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return cfg
	}
	var parsed DailyNotesConfig
	if err := json.Unmarshal(data, &parsed); err == nil {
		if strings.TrimSpace(parsed.Format) != "" {
			cfg.Format = strings.TrimSpace(parsed.Format)
		}
		cfg.Folder = strings.Trim(filepath.ToSlash(strings.TrimSpace(parsed.Folder)), "/")
		cfg.Template = strings.TrimSpace(parsed.Template)
	}
	return cfg
}

// ParseTargetDate parses "today", "yesterday", "tomorrow", integer day offsets ("-1", "+2"),
// or "YYYY-MM-DD" strings into a time.Time.
func ParseTargetDate(dateStr string, now time.Time) (time.Time, error) {
	s := strings.ToLower(strings.TrimSpace(dateStr))
	switch s {
	case "", "today", "now":
		return now, nil
	case "yesterday":
		return now.AddDate(0, 0, -1), nil
	case "tomorrow":
		return now.AddDate(0, 0, 1), nil
	}
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		if offset, err := strconv.Atoi(s); err == nil {
			return now.AddDate(0, 0, offset), nil
		}
	}
	t, err := time.ParseInLocation("2006-01-02", s, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: expected 'today', 'yesterday', 'tomorrow', day offset ('-1', '+1'), or 'YYYY-MM-DD'", dateStr)
	}
	return t, nil
}

// FormatMomentDate converts an Obsidian Moment.js date format string (e.g. "YYYY-MM-DD", "YYYY/MM/YYYY-MM-DD")
// into a formatted date string for t.
func FormatMomentDate(momentFmt string, t time.Time) string {
	if strings.TrimSpace(momentFmt) == "" {
		momentFmt = "YYYY-MM-DD"
	}
	replacer := strings.NewReplacer(
		"YYYY", "2006",
		"YY", "06",
		"MMMM", "January",
		"MMM", "Jan",
		"MM", "01",
		"M", "1",
		"dddd", "Monday",
		"ddd", "Mon",
		"DD", "02",
		"D", "2",
		"HH", "15",
		"mm", "04",
		"ss", "05",
	)
	goLayout := replacer.Replace(momentFmt)
	return t.Format(goLayout)
}

// DailyNotePath computes the vault-relative path for the daily note of date t.
func (v *Vault) DailyNotePath(t time.Time, folderOverride string) (string, DailyNotesConfig) {
	cfg := v.LoadDailyNotesConfig()
	if strings.TrimSpace(folderOverride) != "" {
		cfg.Folder = strings.Trim(filepath.ToSlash(strings.TrimSpace(folderOverride)), "/")
	}
	formatted := FormatMomentDate(cfg.Format, t)
	rel := EnsureMD(formatted)
	if cfg.Folder != "" {
		rel = cfg.Folder + "/" + rel
	}
	return rel, cfg
}

// GetOrCreateDailyNote retrieves the daily note for dateStr, optionally creating it if missing.
func (v *Vault) GetOrCreateDailyNote(dateStr string, createIfMissing bool, folderOverride string) (*NoteDetail, bool, error) {
	now := time.Now()
	targetTime, err := ParseTargetDate(dateStr, now)
	if err != nil {
		return nil, false, err
	}

	relPath, cfg := v.DailyNotePath(targetTime, folderOverride)
	if detail, readErr := v.ReadNote(relPath); readErr == nil {
		return detail, false, nil
	}

	if !createIfMissing {
		return nil, false, fmt.Errorf("%w: daily note %s does not exist yet (set create_if_missing=true to create it)", ErrNoteNotFound, relPath)
	}

	initialContent := fmt.Sprintf("# %s\n\n", targetTime.Format("2006-01-02"))
	if cfg.Template != "" {
		if tplDetail, tplErr := v.ReadNote(cfg.Template); tplErr == nil {
			title := NoteName(relPath)
			r := strings.NewReplacer(
				"{{date}}", targetTime.Format("2006-01-02"),
				"{{time}}", now.Format("15:04"),
				"{{title}}", title,
			)
			initialContent = r.Replace(tplDetail.Content)
		}
	}

	if _, err := v.CreateNote(relPath, initialContent, nil, false); err != nil {
		return nil, false, err
	}
	detail, err := v.ReadNote(relPath)
	return detail, true, err
}

// AppendToDailyNote appends content to a daily note (creating it first if it doesn't exist).
func (v *Vault) AppendToDailyNote(dateStr string, text string, heading string, folderOverride string) (*NoteSummary, error) {
	detail, _, err := v.GetOrCreateDailyNote(dateStr, true, folderOverride)
	if err != nil {
		return nil, err
	}
	return v.AppendNote(detail.Path, text, heading, true)
}

