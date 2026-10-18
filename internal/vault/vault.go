package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrPathTraversal = errors.New("path escapes vault root directory")
	ErrNoteNotFound  = errors.New("note not found in vault")
	ErrNoteExists    = errors.New("note already exists")
)

// Vault represents an Obsidian vault directory on the local filesystem.
type Vault struct {
	root string
}

// New initializes a Vault at the given directory path.
func New(rootPath string) (*Vault, error) {
	if strings.TrimSpace(rootPath) == "" {
		return nil, errors.New("vault path cannot be empty")
	}
	abs, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve vault path: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = realRoot
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("cannot access vault directory %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vault path %q is not a directory", abs)
	}
	return &Vault{root: abs}, nil
}

// Root returns the canonical absolute path of the vault root.
func (v *Vault) Root() string {
	return v.root
}

// EnsureMD appends ".md" if the path does not already have a markdown extension.
func EnsureMD(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	ext := strings.ToLower(filepath.Ext(p))
	if ext == ".md" || ext == ".markdown" || ext == ".canvas" {
		return p
	}
	return p + ".md"
}

// NoteName returns the base note name without directory or ".md" extension.
func NoteName(p string) string {
	base := filepath.Base(filepath.ToSlash(p))
	ext := filepath.Ext(base)
	if strings.EqualFold(ext, ".md") || strings.EqualFold(ext, ".markdown") {
		return base[:len(base)-len(ext)]
	}
	return base
}

// ResolveSafePath validates a relative path inside the vault and returns its absolute path
// and normalized slash-separated vault-relative path. It blocks any path traversal outside the vault.
func (v *Vault) ResolveSafePath(relPath string) (string, string, error) {
	cleaned := filepath.ToSlash(strings.TrimSpace(relPath))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return v.root, "", nil
	}

	abs := filepath.Join(v.root, filepath.FromSlash(cleaned))
	abs, err := filepath.Abs(abs)
	if err != nil {
		return "", "", err
	}

	if !isSubpath(v.root, abs) {
		return "", "", fmt.Errorf("%w: %s", ErrPathTraversal, relPath)
	}

	// Check symlinks on the longest existing ancestor to prevent symlink escape.
	checkPath := abs
	for {
		realPath, err := filepath.EvalSymlinks(checkPath)
		if err == nil {
			if !isSubpath(v.root, realPath) {
				return "", "", fmt.Errorf("%w (via symlink): %s", ErrPathTraversal, relPath)
			}
			break
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath {
			break
		}
		checkPath = parent
	}

	rel, err := filepath.Rel(v.root, abs)
	if err != nil {
		return "", "", err
	}
	return abs, filepath.ToSlash(rel), nil
}

func isSubpath(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	relSlash := filepath.ToSlash(rel)
	if relSlash == ".." || strings.HasPrefix(relSlash, "../") {
		return false
	}
	return true
}

// ResolveNote resolves a note identifier (either a relative path like "folder/note.md",
// "folder/note", or a wikilink-style note name like "note") to its absolute and relative paths.
// If mustExist is true, it searches the vault by basename if the direct path does not exist.
func (v *Vault) ResolveNote(identifier string, mustExist bool) (string, string, error) {
	identifier = strings.TrimSpace(identifier)
	// Strip [[...]] if caller passed a raw wikilink
	if strings.HasPrefix(identifier, "[[") && strings.HasSuffix(identifier, "]]") {
		identifier = strings.TrimPrefix(strings.TrimSuffix(identifier, "]]"), "[[")
		if idx := strings.Index(identifier, "|"); idx != -1 {
			identifier = identifier[:idx]
		}
		if idx := strings.Index(identifier, "#"); idx != -1 {
			identifier = identifier[:idx]
		}
		identifier = strings.TrimSpace(identifier)
	}
	if identifier == "" {
		return "", "", errors.New("note path or name cannot be empty")
	}

	withMD := EnsureMD(identifier)
	abs, rel, err := v.ResolveSafePath(withMD)
	if err != nil {
		return "", "", err
	}

	if !mustExist {
		return abs, rel, nil
	}

	if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
		return abs, rel, nil
	}

	// Fallback: Obsidian-style resolution across the vault.
	targetLower := strings.ToLower(filepath.ToSlash(withMD))
	targetBaseLower := strings.ToLower(filepath.Base(withMD))

	var exactSuffixMatchAbs, exactSuffixMatchRel string
	var caseInsensitiveAbs, caseInsensitiveRel string
	var basenameMatchAbs, basenameMatchRel string

	_ = v.WalkNotes("", func(noteRel string, noteAbs string, _ fs.FileInfo) error {
		noteRelLower := strings.ToLower(noteRel)
		if noteRelLower == targetLower {
			caseInsensitiveAbs = noteAbs
			caseInsensitiveRel = noteRel
			return filepath.SkipAll
		}
		if strings.HasSuffix(noteRelLower, "/"+targetLower) && exactSuffixMatchAbs == "" {
			exactSuffixMatchAbs = noteAbs
			exactSuffixMatchRel = noteRel
		}
		if strings.ToLower(filepath.Base(noteRel)) == targetBaseLower && basenameMatchAbs == "" {
			basenameMatchAbs = noteAbs
			basenameMatchRel = noteRel
		}
		return nil
	})

	if caseInsensitiveAbs != "" {
		return caseInsensitiveAbs, caseInsensitiveRel, nil
	}
	if exactSuffixMatchAbs != "" {
		return exactSuffixMatchAbs, exactSuffixMatchRel, nil
	}
	if basenameMatchAbs != "" {
		return basenameMatchAbs, basenameMatchRel, nil
	}

	return "", "", fmt.Errorf("%w: %s", ErrNoteNotFound, identifier)
}

// IsHiddenOrSystemDir returns true if a directory name should be ignored during vault scans.
func IsHiddenOrSystemDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	if name == "node_modules" {
		return true
	}
	return false
}

// WalkNotes traverses all markdown files (.md, .markdown) in the vault (or a subdirectory),
// skipping hidden directories (.obsidian, .trash, .git, etc.).
func (v *Vault) WalkNotes(subDir string, fn func(relPath string, absPath string, info fs.FileInfo) error) error {
	startAbs := v.root
	if strings.TrimSpace(subDir) != "" {
		var err error
		startAbs, _, err = v.ResolveSafePath(subDir)
		if err != nil {
			return err
		}
	}

	info, err := os.Stat(startAbs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path %q is not a directory", subDir)
	}

	return filepath.WalkDir(startAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // Skip unreadable entries gracefully
		}
		if path == startAbs {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if IsHiddenOrSystemDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".markdown" {
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
		return fn(filepath.ToSlash(rel), path, fInfo)
	})
}

