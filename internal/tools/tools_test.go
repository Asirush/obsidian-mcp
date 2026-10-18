package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/asirush/obsidian-mcp/internal/mcp"
	"github.com/asirush/obsidian-mcp/internal/vault"
)

func TestToolsAndResourcesEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	v, err := vault.New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create vault: %v", err)
	}

	srv := mcp.NewServer("obsidian-mcp", "1.0.0", "")
	RegisterAll(srv, v)

	ctx := context.Background()

	// 1. Verify tools/list returns all 22 tools
	listResp := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if listResp.Error != nil {
		t.Fatalf("tools/list failed: %+v", listResp.Error)
	}
	rawList, _ := json.Marshal(listResp.Result)
	if !strings.Contains(string(rawList), `"read_note"`) || !strings.Contains(string(rawList), `"get_daily_note"`) {
		t.Fatalf("expected tools in tools/list response: %s", string(rawList))
	}

	// 2. Call create_note via tools/call
	createReq := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_note","arguments":{"path":"Notes/Hello.md","content":"Hello #world and [[Second]]!","frontmatter":{"author":"asirush"}}}}`
	createResp := srv.HandleMessage(ctx, []byte(createReq))
	if createResp.Error != nil {
		t.Fatalf("create_note RPC error: %+v", createResp.Error)
	}

	// 3. Call search_notes via tools/call
	searchReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_notes","arguments":{"query":"Hello","tag":"world"}}}`
	searchResp := srv.HandleMessage(ctx, []byte(searchReq))
	if searchResp.Error != nil {
		t.Fatalf("search_notes RPC error: %+v", searchResp.Error)
	}
	rawSearch, _ := json.Marshal(searchResp.Result)
	if !strings.Contains(string(rawSearch), "Notes/Hello.md") {
		t.Fatalf("expected Notes/Hello.md in search results: %s", string(rawSearch))
	}

	// 4. Read note via MCP resources/read
	resReadReq := `{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"obsidian://note/Notes/Hello.md"}}`
	resReadResp := srv.HandleMessage(ctx, []byte(resReadReq))
	if resReadResp.Error != nil {
		t.Fatalf("resources/read error: %+v", resReadResp.Error)
	}
	rawRes, _ := json.Marshal(resReadResp.Result)
	if !strings.Contains(string(rawRes), "Hello #world") {
		t.Fatalf("expected note content in resources/read: %s", string(rawRes))
	}
}

