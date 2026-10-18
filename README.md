# obsidian-mcp (Go)

A fast, zero-plugin **Model Context Protocol (MCP)** server for **Obsidian** vaults written in Go. It operates directly on your vault filesystem—meaning it works whether the Obsidian app is open or closed, compiles to a single static binary, and enforces strict path-traversal protection.

---

## Features

- **Note CRUD & Editing (8 tools)**
  - `read_note` — Read a note by relative path (`Projects/Idea.md`) or wikilink name (`[[Idea]]`), returning markdown content, parsed YAML frontmatter, tags, outgoing links, and metadata.
  - `read_multiple_notes` — Batch-read multiple notes in one call.
  - `create_note` — Create a note with optional YAML frontmatter and automatic directory creation.
  - `update_note` — Update note content and/or frontmatter (preserving existing frontmatter by default).
  - `append_note` — Append markdown to the end of a note or directly under a specific heading (e.g., `## Tasks`).
  - `patch_note` — Exact or regex search-and-replace inside a note.
  - `delete_note` — Moves notes to `.trash/` by default (Obsidian-style safe delete), or permanently deletes when `permanent: true`.
  - `move_note` — Rename/move a note and automatically rewrite `[[wikilinks]]` across the entire vault.

- **Search & Navigation (3 tools)**
  - `list_notes` — List notes and folders (recursive or top-level).
  - `search_notes` — Full-text or regex search with surrounding context lines, case-sensitivity control, folder scope, and tag filtering.
  - `find_notes_by_name` — Fast filename/path substring or glob (`*meeting*`) search.

- **YAML Frontmatter & Tags (4 tools)**
  - `get_frontmatter` — Read parsed YAML properties and all tags (both YAML `tags` and inline `#tags`, ignoring code blocks).
  - `update_frontmatter` — Set, merge, or unset frontmatter properties without modifying the markdown body.
  - `list_tags` — List all tags across the vault with usage counts and optional note paths.
  - `search_by_tag` — Find notes matching one or more tags (including hierarchical child tags like `#project/active` when searching `#project`).

- **Links, Backlinks & Knowledge Graph (5 tools)**
  - `get_outgoing_links` — Parse all `[[wikilinks]]`, `![[embeds]]`, and `[markdown](links.md)` and check if each target exists.
  - `get_backlinks` — Find all notes linking to a target note with line numbers and snippets.
  - `get_broken_links` — Scan the vault for unresolved `[[wikilinks]]` and broken internal links.
  - `get_orphan_notes` — Find notes with zero incoming backlinks.
  - `get_vault_stats` — Overview of total notes, folders, tags, links, broken links, orphans, and recently modified notes.

- **Daily / Periodic Notes (2 tools)**
  - `get_daily_note` — Get or create a daily note (`today`, `yesterday`, `tomorrow`, day offsets like `-1`/`+1`, or `YYYY-MM-DD`). Automatically reads `.obsidian/daily-notes.json` (`folder`, `format`, `template`) and expands `{{date}}`, `{{time}}`, and `{{title}}`.
  - `append_to_daily_note` — Append text (optionally under a specific heading like `## Tasks` or `## Log`) to a daily note, creating it from your template if it doesn't exist yet.

- **MCP Resources & Prompts**
  - Exposes `obsidian://note/{path}` resources (`resources/list`, `resources/templates/list`, `resources/read`).
  - Built-in `summarize_note` prompt that bundles a note's content and its incoming backlinks.

---

## Build

```bash
/usr/local/go/bin/go build -o bin/obsidian-mcp ./cmd/obsidian-mcp
```

Run tests:

```bash
/usr/local/go/bin/go test -v -race ./...
```

---

## Configuration (Stdio MCP Clients: Claude Desktop, Cursor, Antigravity)

You can pass your vault path either via the `--vault` flag, as a positional argument, or via the `OBSIDIAN_VAULT_PATH` environment variable:

```json
{
  "mcpServers": {
    "obsidian": {
      "command": "/bin/obsidian-mcp",
      "args": ["--vault", "/path/to/your/obsidian-vault"]
    }
  }
}
```

---

## Connecting to Open WebUI + Ollama

`obsidian-mcp` includes a **built-in HTTP server** (`--http :8080`) that simultaneously serves:
1. **OpenAPI 3.1 Tool Server** (`http://localhost:8080/openapi.json` + `/tools/{tool_name}`) — works natively with all versions of Open WebUI (**no `mcpo` proxy needed**).
2. **MCP Streamable HTTP** (`http://localhost:8080/mcp`) — works with Open WebUI's native MCP Streamable HTTP mode.

### 1. Start `obsidian-mcp` in HTTP Mode

```bash
./bin/obsidian-mcp \
  --vault /path/to/your/obsidian-vault \
  --http 0.0.0.0:8080
```

*(Optional)* Add `--api-key your-secret-token` if you want Bearer authentication.

### 2. Add to Open WebUI

1. Open **Open WebUI** -> **Admin Panel** (or **Settings**) -> **External Tools** (or **Tools**).
2. Click **+ (Add Server)**:
   - **Option A — OpenAPI (Recommended, works across all Open WebUI versions):**
     - **Type**: `OpenAPI`
     - **URL**: `http://localhost:8080` *(or `http://host.docker.internal:8080` if Open WebUI runs inside Docker)*
     - **OpenAPI Spec Path**: `openapi.json`
   - **Option B — Native MCP (Open WebUI v0.6.31+):**
     - **Type**: `MCP Streamable HTTP`
     - **URL**: `http://localhost:8080/mcp`
3. Click **Save**.

### 3. Enable Native Function Calling for Your Ollama Model

For Ollama models to reliably invoke the 22 Obsidian tools in Open WebUI:
1. Pick a tool-capable Ollama model (e.g., `qwen2.5:14b`, `qwen3:8b`, `llama3.1:8b`, `mistral-nemo`, `command-r`).
2. In a chat, click the **+** / **Tools** icon next to the message input and toggle **obsidian-mcp** on.
3. In **Chat Controls (top-right sliders) -> Advanced Params**, set **Function Calling** to **`Native`** (instead of `Default`), or set it globally under **Workspace -> Models -> Edit Model -> Advanced Params -> Function Calling: Native**.

---

## Running with Docker Compose

1. Copy `.env.example` to `.env` and set `OBSIDIAN_VAULT_PATH` to your vault directory on the host:

```bash
cp .env.example .env
# Edit .env and set OBSIDIAN_VAULT_PATH=/path/to/your/obsidian-vault
```

2. Build and start the container in the background:

```bash
docker compose up -d --build
```

The container exposes:
- `http://localhost:8080/openapi.json` (OpenAPI 3.1 Tool Server for Open WebUI)
- `http://localhost:8080/mcp` (MCP Streamable HTTP)
- `http://localhost:8080/health` (Health check)

You can also connect a `stdio`-based MCP client directly to the running container using `docker exec`:

```json
{
  "mcpServers": {
    "obsidian": {
      "command": "docker",
      "args": ["exec", "-i", "obsidian-mcp", "/usr/local/bin/obsidian-mcp", "--vault", "/vault"]
    }
  }
}
```


