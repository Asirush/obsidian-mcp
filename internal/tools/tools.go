package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/asirush/obsidian-mcp/internal/mcp"
	"github.com/asirush/obsidian-mcp/internal/vault"
)

// RegisterAll registers all Obsidian tools, resources, and prompts on the MCP server.
func RegisterAll(srv *mcp.Server, v *vault.Vault) {
	registerNoteCRUDTools(srv, v)
	registerSearchAndNavTools(srv, v)
	registerFrontmatterAndTagTools(srv, v)
	registerGraphAndLinkTools(srv, v)
	registerDailyNoteTools(srv, v)
	registerResources(srv, v)
	registerPrompts(srv, v)
}

func registerNoteCRUDTools(srv *mcp.Server, v *vault.Vault) {
	// 1. read_note
	srv.RegisterTool(mcp.Tool{
		Name:        "read_note",
		Description: "Read a note from the Obsidian vault by relative path (e.g. 'Projects/Roadmap.md') or wikilink name (e.g. 'Roadmap'). Returns markdown content, parsed YAML frontmatter, tags, outgoing links, and file metadata.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Vault-relative path or note name (e.g. 'Notes/Idea.md', 'Notes/Idea', or '[[Idea]]')",
				},
			},
			Required: []string{"path"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Read Note",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		detail, err := v.ReadNote(path)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(detail)
	})

	// 2. read_multiple_notes
	srv.RegisterTool(mcp.Tool{
		Name:        "read_multiple_notes",
		Description: "Read multiple Obsidian notes in a single batch call. Each path is resolved independently so one missing note does not fail the entire batch.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"paths": {
					Type:        "array",
					Description: "List of note paths or note names to read",
					Items:       &mcp.Property{Type: "string"},
				},
			},
			Required: []string{"paths"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Read Multiple Notes",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		paths := mcp.GetStringSlice(args, "paths")
		if len(paths) == 0 {
			return mcp.NewErrorResult("paths array cannot be empty")
		}
		type itemResult struct {
			Query string            `json:"query"`
			Note  *vault.NoteDetail `json:"note,omitempty"`
			Error string            `json:"error,omitempty"`
		}
		results := make([]itemResult, 0, len(paths))
		for _, p := range paths {
			detail, err := v.ReadNote(p)
			if err != nil {
				results = append(results, itemResult{Query: p, Error: err.Error()})
			} else {
				results = append(results, itemResult{Query: p, Note: detail})
			}
		}
		return mcp.NewJSONResult(map[string]any{"results": results})
	})

	// 3. create_note
	srv.RegisterTool(mcp.Tool{
		Name:        "create_note",
		Description: "Create a new markdown note in the Obsidian vault with optional YAML frontmatter. Automatically creates parent directories if needed.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Vault-relative path for the new note (e.g. 'Projects/New Project.md')",
				},
				"content": {
					Type:        "string",
					Description: "Markdown body content of the note",
				},
				"frontmatter": {
					Type:                 "object",
					Description:          "Optional YAML frontmatter properties as key-value pairs (e.g. {\"tags\": [\"project\"], \"status\": \"active\"})",
					AdditionalProperties: true,
				},
				"overwrite": {
					Type:        "boolean",
					Description: "If true, overwrite the note if it already exists (default: false)",
					Default:     false,
				},
			},
			Required: []string{"path", "content"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:           "Create Note",
			ReadOnlyHint:    mcp.BoolPtr(false),
			DestructiveHint: mcp.BoolPtr(false),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		content := mcp.GetString(args, "content", "")
		fm := mcp.GetMap(args, "frontmatter")
		overwrite := mcp.GetBool(args, "overwrite", false)

		summary, err := v.CreateNote(path, content, fm, overwrite)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(summary)
	})

	// 4. update_note
	srv.RegisterTool(mcp.Tool{
		Name:        "update_note",
		Description: "Update the content and/or YAML frontmatter of an existing note in the Obsidian vault.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Vault-relative path or note name to update",
				},
				"content": {
					Type:        "string",
					Description: "New markdown content for the note",
				},
				"frontmatter": {
					Type:                 "object",
					Description:          "Optional YAML frontmatter key-value pairs to merge or set",
					AdditionalProperties: true,
				},
				"preserve_frontmatter": {
					Type:        "boolean",
					Description: "Keep existing YAML frontmatter if the new content does not specify frontmatter (default: true)",
					Default:     true,
				},
				"create_if_missing": {
					Type:        "boolean",
					Description: "Create the note if it does not already exist (default: false)",
					Default:     false,
				},
			},
			Required: []string{"path", "content"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:          "Update Note",
			ReadOnlyHint:   mcp.BoolPtr(false),
			IdempotentHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		content := mcp.GetString(args, "content", "")
		fm := mcp.GetMap(args, "frontmatter")
		preserveFM := mcp.GetBool(args, "preserve_frontmatter", true)
		createIfMissing := mcp.GetBool(args, "create_if_missing", false)

		summary, err := v.UpdateNote(path, content, fm, preserveFM, createIfMissing)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(summary)
	})

	// 5. append_note
	srv.RegisterTool(mcp.Tool{
		Name:        "append_note",
		Description: "Append markdown content to the end of a note, or under a specific markdown heading (e.g. '## Tasks' or 'Log').",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Vault-relative path or note name",
				},
				"content": {
					Type:        "string",
					Description: "Markdown text to append",
				},
				"heading": {
					Type:        "string",
					Description: "Optional heading title to append under (e.g. '## Tasks' or 'Tasks'). If omitted, appends to the end of the file.",
				},
				"create_if_missing": {
					Type:        "boolean",
					Description: "Create the note if it does not exist (default: false)",
					Default:     false,
				},
			},
			Required: []string{"path", "content"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Append to Note",
			ReadOnlyHint: mcp.BoolPtr(false),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		content := mcp.GetString(args, "content", "")
		heading := mcp.GetString(args, "heading", "")
		createIfMissing := mcp.GetBool(args, "create_if_missing", false)

		summary, err := v.AppendNote(path, content, heading, createIfMissing)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(summary)
	})

	// 6. patch_note
	srv.RegisterTool(mcp.Tool{
		Name:        "patch_note",
		Description: "Perform targeted search-and-replace (exact string or regular expression) inside a note without rewriting the entire document.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Vault-relative path or note name",
				},
				"search": {
					Type:        "string",
					Description: "Exact text or regex pattern to find",
				},
				"replace": {
					Type:        "string",
					Description: "Replacement text",
				},
				"use_regex": {
					Type:        "boolean",
					Description: "Treat 'search' as a regular expression (default: false)",
					Default:     false,
				},
				"replace_all": {
					Type:        "boolean",
					Description: "Replace all occurrences instead of only the first match (default: false)",
					Default:     false,
				},
			},
			Required: []string{"path", "search", "replace"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Patch Note",
			ReadOnlyHint: mcp.BoolPtr(false),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		search := mcp.GetString(args, "search", "")
		replace := mcp.GetString(args, "replace", "")
		useRegex := mcp.GetBool(args, "use_regex", false)
		replaceAll := mcp.GetBool(args, "replace_all", false)

		rel, count, err := v.PatchNote(path, search, replace, useRegex, replaceAll)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"path":         rel,
			"replacements": count,
		})
	})

	// 7. delete_note
	srv.RegisterTool(mcp.Tool{
		Name:        "delete_note",
		Description: "Delete a note from the Obsidian vault. By default moves the note to the vault's '.trash/' folder (safe delete) unless permanent=true.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Vault-relative path or note name to delete",
				},
				"permanent": {
					Type:        "boolean",
					Description: "If true, permanently delete the file instead of moving to .trash/ (default: false)",
					Default:     false,
				},
			},
			Required: []string{"path"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:           "Delete Note",
			ReadOnlyHint:    mcp.BoolPtr(false),
			DestructiveHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		permanent := mcp.GetBool(args, "permanent", false)

		rel, dest, err := v.DeleteNote(path, permanent)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"deleted_path": rel,
			"destination":  dest,
			"permanent":    permanent,
		})
	})

	// 8. move_note
	srv.RegisterTool(mcp.Tool{
		Name:        "move_note",
		Description: "Rename or move a note to a new path in the vault, and optionally update all [[wikilinks]] referencing it across the vault.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"source": {
					Type:        "string",
					Description: "Current vault-relative path or note name",
				},
				"destination": {
					Type:        "string",
					Description: "New vault-relative path for the note",
				},
				"update_links": {
					Type:        "boolean",
					Description: "Automatically rewrite [[wikilinks]] across the vault to point to the new note name/path (default: true)",
					Default:     true,
				},
				"overwrite": {
					Type:        "boolean",
					Description: "Overwrite destination if it already exists (default: false)",
					Default:     false,
				},
			},
			Required: []string{"source", "destination"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Move or Rename Note",
			ReadOnlyHint: mcp.BoolPtr(false),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		src := mcp.GetString(args, "source", "")
		dst := mcp.GetString(args, "destination", "")
		updateLinks := mcp.GetBool(args, "update_links", true)
		overwrite := mcp.GetBool(args, "overwrite", false)

		res, err := v.MoveNote(src, dst, overwrite, updateLinks)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(res)
	})
}

func registerSearchAndNavTools(srv *mcp.Server, v *vault.Vault) {
	// 9. list_notes
	srv.RegisterTool(mcp.Tool{
		Name:        "list_notes",
		Description: "List markdown notes and folders in the Obsidian vault or within a specific subfolder.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"folder": {
					Type:        "string",
					Description: "Optional vault-relative subfolder path (default: root of vault)",
				},
				"recursive": {
					Type:        "boolean",
					Description: "Recursively list all nested subfolders and notes (default: true)",
					Default:     true,
				},
			},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "List Notes",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		folder := mcp.GetString(args, "folder", "")
		recursive := mcp.GetBool(args, "recursive", true)

		entries, err := v.ListNotes(folder, recursive)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"folder":  folder,
			"count":   len(entries),
			"entries": entries,
		})
	})

	// 10. search_notes
	srv.RegisterTool(mcp.Tool{
		Name:        "search_notes",
		Description: "Search across all notes in the Obsidian vault by full-text substring or regular expression, with optional folder and tag filters.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"query": {
					Type:        "string",
					Description: "Text substring or regular expression pattern to search for",
				},
				"use_regex": {
					Type:        "boolean",
					Description: "Interpret 'query' as a regular expression (default: false)",
					Default:     false,
				},
				"case_sensitive": {
					Type:        "boolean",
					Description: "Perform case-sensitive matching (default: false)",
					Default:     false,
				},
				"folder": {
					Type:        "string",
					Description: "Optional vault-relative folder to restrict search scope",
				},
				"tag": {
					Type:        "string",
					Description: "Optional tag (e.g. 'project' or '#project/active') to filter notes before searching content",
				},
				"context_lines": {
					Type:        "integer",
					Description: "Number of surrounding context lines to include around each match (0-10, default: 1)",
					Default:     1,
				},
				"limit": {
					Type:        "integer",
					Description: "Maximum number of matching notes to return (default: 50)",
					Default:     50,
				},
			},
			Required: []string{"query"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Search Notes Content",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		opts := vault.SearchOptions{
			Query:         mcp.GetString(args, "query", ""),
			UseRegex:      mcp.GetBool(args, "use_regex", false),
			CaseSensitive: mcp.GetBool(args, "case_sensitive", false),
			Folder:        mcp.GetString(args, "folder", ""),
			Tag:           mcp.GetString(args, "tag", ""),
			ContextLines:  mcp.GetInt(args, "context_lines", 1),
			Limit:         mcp.GetInt(args, "limit", 50),
		}
		results, err := v.SearchNotes(opts)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"query":        opts.Query,
			"result_count": len(results),
			"results":      results,
		})
	})

	// 11. find_notes_by_name
	srv.RegisterTool(mcp.Tool{
		Name:        "find_notes_by_name",
		Description: "Find notes by filename or path using case-insensitive substring matching or glob patterns (e.g. '*meeting*').",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"query": {
					Type:        "string",
					Description: "Filename substring or glob pattern",
				},
				"folder": {
					Type:        "string",
					Description: "Optional folder to restrict search",
				},
				"limit": {
					Type:        "integer",
					Description: "Maximum number of matches to return (default: 50)",
					Default:     50,
				},
			},
			Required: []string{"query"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Find Notes by Name",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		query := mcp.GetString(args, "query", "")
		folder := mcp.GetString(args, "folder", "")
		limit := mcp.GetInt(args, "limit", 50)

		matches, err := v.FindNotesByName(query, folder, limit)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"query":   query,
			"count":   len(matches),
			"matches": matches,
		})
	})
}

func registerFrontmatterAndTagTools(srv *mcp.Server, v *vault.Vault) {
	// 12. get_frontmatter
	srv.RegisterTool(mcp.Tool{
		Name:        "get_frontmatter",
		Description: "Get the parsed YAML frontmatter properties and extracted tags of a note.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Vault-relative path or note name",
				},
			},
			Required: []string{"path"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Get Note Frontmatter",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		rel, fm, tags, err := v.GetFrontmatter(path)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"path":        rel,
			"frontmatter": fm,
			"tags":        tags,
		})
	})

	// 13. update_frontmatter
	srv.RegisterTool(mcp.Tool{
		Name:        "update_frontmatter",
		Description: "Set, merge, or remove YAML frontmatter keys on a note without modifying its markdown body.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Vault-relative path or note name",
				},
				"set": {
					Type:                 "object",
					Description:          "Key-value pairs to set or update in the YAML frontmatter",
					AdditionalProperties: true,
				},
				"unset": {
					Type:        "array",
					Description: "List of frontmatter property keys to remove",
					Items:       &mcp.Property{Type: "string"},
				},
			},
			Required: []string{"path"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:          "Update Note Frontmatter",
			ReadOnlyHint:   mcp.BoolPtr(false),
			IdempotentHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		updates := mcp.GetMap(args, "set")
		deleteKeys := mcp.GetStringSlice(args, "unset")

		rel, fm, err := v.UpdateFrontmatter(path, updates, deleteKeys)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"path":        rel,
			"frontmatter": fm,
		})
	})

	// 14. list_tags
	srv.RegisterTool(mcp.Tool{
		Name:        "list_tags",
		Description: "List all tags used across the Obsidian vault (both YAML frontmatter tags and inline #tags) with usage counts.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"folder": {
					Type:        "string",
					Description: "Optional folder to restrict tag scan",
				},
				"prefix": {
					Type:        "string",
					Description: "Optional tag prefix filter (e.g. 'project/' or 'status')",
				},
				"include_notes": {
					Type:        "boolean",
					Description: "Include the list of note paths for each tag (default: false)",
					Default:     false,
				},
			},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "List Vault Tags",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		folder := mcp.GetString(args, "folder", "")
		prefix := mcp.GetString(args, "prefix", "")
		includeNotes := mcp.GetBool(args, "include_notes", false)

		tags, err := v.ListTags(folder, prefix, includeNotes)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"total_tags": len(tags),
			"tags":       tags,
		})
	})

	// 15. search_by_tag
	srv.RegisterTool(mcp.Tool{
		Name:        "search_by_tag",
		Description: "Find all notes that contain one or more specified tags (supports hierarchical tags, e.g. 'project' matches 'project/alpha').",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"tags": {
					Type:        "array",
					Description: "Tags to search for (with or without leading '#')",
					Items:       &mcp.Property{Type: "string"},
				},
				"match_all": {
					Type:        "boolean",
					Description: "If true, require notes to match ALL specified tags (AND); if false, match ANY tag (OR) (default: true)",
					Default:     true,
				},
				"folder": {
					Type:        "string",
					Description: "Optional folder to restrict search",
				},
			},
			Required: []string{"tags"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Search Notes by Tag",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		tags := mcp.GetStringSlice(args, "tags")
		matchAll := mcp.GetBool(args, "match_all", true)
		folder := mcp.GetString(args, "folder", "")

		matches, err := v.SearchByTag(tags, matchAll, folder)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"query_tags": tags,
			"match_all":  matchAll,
			"count":      len(matches),
			"notes":      matches,
		})
	})
}

func registerGraphAndLinkTools(srv *mcp.Server, v *vault.Vault) {
	// 16. get_outgoing_links
	srv.RegisterTool(mcp.Tool{
		Name:        "get_outgoing_links",
		Description: "Extract all outgoing [[wikilinks]], ![[embeds]], and internal [markdown](links.md) from a note, showing whether each target exists in the vault.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Vault-relative path or note name",
				},
			},
			Required: []string{"path"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Get Outgoing Links",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		rel, links, err := v.GetOutgoingLinks(path)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"path":           rel,
			"outgoing_count": len(links),
			"outgoing_links": links,
		})
	})

	// 17. get_backlinks
	srv.RegisterTool(mcp.Tool{
		Name:        "get_backlinks",
		Description: "Find all notes in the vault that link to the specified note (incoming backlinks), including line numbers and surrounding context.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path": {
					Type:        "string",
					Description: "Target note path or note name to find backlinks for",
				},
			},
			Required: []string{"path"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Get Backlinks",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		path := mcp.GetString(args, "path", "")
		rel, backlinks, err := v.GetBacklinks(path)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"target_path":    rel,
			"backlink_count": len(backlinks),
			"backlinks":      backlinks,
		})
	})

	// 18. get_broken_links
	srv.RegisterTool(mcp.Tool{
		Name:        "get_broken_links",
		Description: "Find all unresolved [[wikilinks]] and broken internal links across the vault (or within a specific folder).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"folder": {
					Type:        "string",
					Description: "Optional vault-relative folder to restrict scan",
				},
			},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Get Broken Links",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		folder := mcp.GetString(args, "folder", "")
		broken, err := v.GetBrokenLinks(folder)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"broken_count": len(broken),
			"broken_links": broken,
		})
	})

	// 19. get_orphan_notes
	srv.RegisterTool(mcp.Tool{
		Name:        "get_orphan_notes",
		Description: "Find all orphan notes in the vault that have zero incoming backlinks from other notes.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"folder": {
					Type:        "string",
					Description: "Optional vault-relative folder to restrict scan",
				},
			},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Get Orphan Notes",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		folder := mcp.GetString(args, "folder", "")
		orphans, err := v.GetOrphanNotes(folder)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"orphan_count": len(orphans),
			"orphan_notes": orphans,
		})
	})

	// 20. get_vault_stats
	srv.RegisterTool(mcp.Tool{
		Name:        "get_vault_stats",
		Description: "Get summary statistics of the Obsidian vault including total notes, folders, tags, links, broken links, orphan notes, and recently modified notes.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]mcp.Property{},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Get Vault Statistics",
			ReadOnlyHint: mcp.BoolPtr(true),
		},
	}, func(_ context.Context, _ map[string]any) mcp.CallToolResult {
		stats, err := v.GetVaultStats()
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(stats)
	})
}

func registerDailyNoteTools(srv *mcp.Server, v *vault.Vault) {
	// 21. get_daily_note
	srv.RegisterTool(mcp.Tool{
		Name:        "get_daily_note",
		Description: "Get (or create) an Obsidian daily note for 'today', 'yesterday', 'tomorrow', a day offset ('-1', '+1'), or a specific date ('YYYY-MM-DD'). Respects .obsidian/daily-notes.json configuration and templates.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"date": {
					Type:        "string",
					Description: "Target date: 'today' (default), 'yesterday', 'tomorrow', day offset ('-1', '+2'), or 'YYYY-MM-DD'",
					Default:     "today",
				},
				"create_if_missing": {
					Type:        "boolean",
					Description: "Create the daily note (applying template if configured) if it does not exist yet (default: true)",
					Default:     true,
				},
				"folder": {
					Type:        "string",
					Description: "Optional folder override for daily notes (otherwise uses .obsidian/daily-notes.json or vault root)",
				},
			},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Get or Create Daily Note",
			ReadOnlyHint: mcp.BoolPtr(false),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		dateStr := mcp.GetString(args, "date", "today")
		createIfMissing := mcp.GetBool(args, "create_if_missing", true)
		folder := mcp.GetString(args, "folder", "")

		detail, created, err := v.GetOrCreateDailyNote(dateStr, createIfMissing, folder)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(map[string]any{
			"created": created,
			"note":    detail,
		})
	})

	// 22. append_to_daily_note
	srv.RegisterTool(mcp.Tool{
		Name:        "append_to_daily_note",
		Description: "Append text or a task/log entry to a daily note (creating the daily note first if it doesn't exist), optionally under a specific heading.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"content": {
					Type:        "string",
					Description: "Markdown content to append to the daily note",
				},
				"date": {
					Type:        "string",
					Description: "Target date: 'today' (default), 'yesterday', 'tomorrow', day offset, or 'YYYY-MM-DD'",
					Default:     "today",
				},
				"heading": {
					Type:        "string",
					Description: "Optional markdown heading to append under (e.g. '## Tasks' or '## Log')",
				},
				"folder": {
					Type:        "string",
					Description: "Optional folder override for daily notes",
				},
			},
			Required: []string{"content"},
		},
		Annotations: &mcp.ToolAnnotation{
			Title:        "Append to Daily Note",
			ReadOnlyHint: mcp.BoolPtr(false),
		},
	}, func(_ context.Context, args map[string]any) mcp.CallToolResult {
		content := mcp.GetString(args, "content", "")
		dateStr := mcp.GetString(args, "date", "today")
		heading := mcp.GetString(args, "heading", "")
		folder := mcp.GetString(args, "folder", "")

		summary, err := v.AppendToDailyNote(dateStr, content, heading, folder)
		if err != nil {
			return mcp.NewErrorResult(err.Error())
		}
		return mcp.NewJSONResult(summary)
	})
}

func registerResources(srv *mcp.Server, v *vault.Vault) {
	templates := []mcp.ResourceTemplate{
		{
			URITemplate: "obsidian://note/{path}",
			Name:        "Obsidian Vault Note",
			Description: "Access any markdown note in the Obsidian vault by relative path",
			MimeType:    "text/markdown",
		},
	}

	listFn := func(_ context.Context) ([]mcp.Resource, error) {
		entries, err := v.ListNotes("", true)
		if err != nil {
			return nil, err
		}
		var resources []mcp.Resource
		for _, e := range entries {
			if e.IsDir {
				continue
			}
			resources = append(resources, mcp.Resource{
				URI:         "obsidian://note/" + e.Path,
				Name:        e.Name,
				Description: fmt.Sprintf("Obsidian note at %s (%d bytes)", e.Path, e.Size),
				MimeType:    "text/markdown",
			})
		}
		return resources, nil
	}

	readFn := func(_ context.Context, uri string) ([]mcp.ResourceContent, error) {
		const prefix = "obsidian://note/"
		if !strings.HasPrefix(uri, prefix) {
			return nil, fmt.Errorf("unsupported resource URI %q (expected prefix %s)", uri, prefix)
		}
		rawPath := strings.TrimPrefix(uri, prefix)
		if decoded, err := url.PathUnescape(rawPath); err == nil {
			rawPath = decoded
		}
		detail, err := v.ReadNote(rawPath)
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContent{
			{
				URI:      "obsidian://note/" + detail.Path,
				MimeType: "text/markdown",
				Text:     detail.Content,
			},
		}, nil
	}

	srv.SetResourceHandlers(templates, listFn, readFn)
}

func registerPrompts(srv *mcp.Server, v *vault.Vault) {
	srv.RegisterPrompt(mcp.Prompt{
		Name:        "summarize_note",
		Description: "Read an Obsidian note along with its outgoing links and backlinks to generate a comprehensive summary.",
		Arguments: []mcp.PromptArgument{
			{
				Name:        "path",
				Description: "Vault-relative path or note name to summarize",
				Required:    true,
			},
		},
	}, func(_ context.Context, args map[string]string) (*mcp.GetPromptResult, error) {
		path := args["path"]
		detail, err := v.ReadNote(path)
		if err != nil {
			return nil, err
		}
		_, backlinks, _ := v.GetBacklinks(detail.Path)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Please summarize the following Obsidian note (%s):\n\n", detail.Path))
		sb.WriteString("```markdown\n")
		sb.WriteString(detail.Content)
		sb.WriteString("\n```\n\n")
		if len(backlinks) > 0 {
			sb.WriteString("Notes linking to this note (Backlinks):\n")
			for _, bl := range backlinks {
				sb.WriteString(fmt.Sprintf("- %s (line %d): %s\n", bl.SourcePath, bl.Line, bl.Context))
			}
		}

		return &mcp.GetPromptResult{
			Description: fmt.Sprintf("Summary prompt for %s", detail.Path),
			Messages: []mcp.PromptMessage{
				{
					Role: "user",
					Content: mcp.ContentItem{
						Type: "text",
						Text: sb.String(),
					},
				},
			},
		}, nil
	})
}

