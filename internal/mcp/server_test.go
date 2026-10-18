package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerLifecycleAndFraming(t *testing.T) {
	srv := NewServer("test-mcp", "1.0.0", "test instructions")
	srv.RegisterTool(Tool{
		Name:        "echo",
		Description: "Echo input",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"msg": {Type: "string"},
			},
			Required: []string{"msg"},
		},
	}, func(_ context.Context, args map[string]any) CallToolResult {
		return NewJSONResult(map[string]string{"echoed": GetString(args, "msg", "")})
	})

	inputLines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"test-client","version":"0.1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hello"}}}`,
	}

	// Also append a Content-Length framed message
	framedBody := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"framed"}}}`
	framedMsg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(framedBody), framedBody)

	rawInput := strings.Join(inputLines, "\n") + "\n" + framedMsg
	var out bytes.Buffer

	if err := srv.Serve(context.Background(), strings.NewReader(rawInput), &out); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	respLines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// 5 requests with IDs (notification produces no response)
	if len(respLines) != 5 {
		t.Fatalf("expected 5 responses, got %d:\n%s", len(respLines), out.String())
	}

	var callResp Response
	if err := json.Unmarshal([]byte(respLines[4]), &callResp); err != nil {
		t.Fatalf("failed to unmarshal response 5: %v", err)
	}
	if callResp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", callResp.Error)
	}
	rawRes, _ := json.Marshal(callResp.Result)
	if !strings.Contains(string(rawRes), "framed") {
		t.Fatalf("expected 'framed' in result, got: %s", string(rawRes))
	}
}

func TestOpenWebUIHTTPAndOpenAPI(t *testing.T) {
	srv := NewServer("obsidian-mcp", "1.0.0", "instructions")
	srv.RegisterTool(Tool{
		Name:        "echo",
		Description: "Echo input",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"msg": {Type: "string"},
			},
			Required: []string{"msg"},
		},
	}, func(_ context.Context, args map[string]any) CallToolResult {
		return NewJSONResult(map[string]string{"echoed": GetString(args, "msg", "")})
	})

	handler := srv.Handler(HTTPConfig{APIKey: "secret123"})

	// 1. Test /openapi.json with Bearer token
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/openapi.json", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /openapi.json, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"/tools/echo"`) {
		t.Fatalf("expected /tools/echo in OpenAPI spec, got: %s", rec.Body.String())
	}

	// 2. Test POST /tools/echo (Open WebUI OpenAPI Tool Call)
	req2 := httptest.NewRequest(http.MethodPost, "http://localhost:8080/tools/echo", strings.NewReader(`{"msg":"openwebui"}`))
	req2.Header.Set("Authorization", "Bearer secret123")
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for POST /tools/echo, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"echoed": "openwebui"`) {
		t.Fatalf("unexpected body: %s", rec2.Body.String())
	}

	// 3. Test POST /mcp (MCP Streamable HTTP)
	req3 := httptest.NewRequest(http.MethodPost, "http://localhost:8080/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req3.Header.Set("Authorization", "Bearer secret123")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for POST /mcp, got %d", rec3.Code)
	}
}

