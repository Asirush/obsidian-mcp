package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HTTPConfig configures the HTTP server for Open WebUI (OpenAPI + MCP Streamable HTTP).
type HTTPConfig struct {
	Addr      string
	ServerURL string
	APIKey    string
}

// Handler returns an http.Handler serving:
//   - GET  /openapi.json      (OpenAPI 3.1 spec for Open WebUI External Tools)
//   - POST /tools/{tool_name} (Direct REST tool execution for Open WebUI)
//   - POST /mcp               (MCP Streamable HTTP JSON-RPC 2.0 endpoint)
//   - GET  /health            (Health check)
func (s *Server) Handler(cfg HTTPConfig) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"name":    s.name,
			"version": s.version,
		})
	})

	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		spec := s.BuildOpenAPISpec(cfg.ServerURL, r)
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(spec)
	})

	mux.HandleFunc("/tools/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		toolName := strings.TrimPrefix(r.URL.Path, "/tools/")
		toolName = strings.Trim(toolName, "/")
		s.handleRESTToolCall(w, r, toolName)
	})

	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resp := s.HandleMessage(r.Context(), body)
			if resp == nil {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)

		default:
			http.Error(w, "use POST /mcp for MCP Streamable HTTP JSON-RPC requests", http.StatusMethodNotAllowed)
		}
	})

	return withCORSAndAuth(mux, cfg.APIKey)
}

func (s *Server) handleRESTToolCall(w http.ResponseWriter, r *http.Request, toolName string) {
	s.mu.RLock()
	idx, ok := s.toolMap[toolName]
	var handler ToolHandler
	if ok {
		handler = s.tools[idx].handler
	}
	s.mu.RUnlock()

	if !ok || handler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf("unknown tool: %s", toolName),
		})
		return
	}

	var args map[string]any
	body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &args); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid JSON body: " + err.Error()})
			return
		}
	}
	if args == nil {
		args = make(map[string]any)
	}

	res := handler(r.Context(), args)
	w.Header().Set("Content-Type", "application/json")
	if res.IsError {
		w.WriteHeader(http.StatusBadRequest)
		errText := "tool error"
		if len(res.Content) > 0 {
			errText = res.Content[0].Text
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"error": errText})
		return
	}

	if len(res.Content) == 1 && res.Content[0].Type == "text" {
		rawText := strings.TrimSpace(res.Content[0].Text)
		if json.Valid([]byte(rawText)) {
			_, _ = w.Write([]byte(rawText))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": rawText})
		return
	}

	_ = json.NewEncoder(w).Encode(res)
}

// BuildOpenAPISpec dynamically converts all registered MCP tools into an OpenAPI 3.1.0 specification
// compatible with Open WebUI's native OpenAPI Tool Server integration.
func (s *Server) BuildOpenAPISpec(configuredServerURL string, r *http.Request) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	serverURL := strings.TrimRight(configuredServerURL, "/")
	if serverURL == "" && r != nil && r.Host != "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		serverURL = fmt.Sprintf("%s://%s", scheme, r.Host)
	}

	paths := make(map[string]any, len(s.tools))
	for _, rt := range s.tools {
		t := rt.tool
		schema := map[string]any{
			"type":       "object",
			"properties": t.InputSchema.Properties,
		}
		if len(t.InputSchema.Required) > 0 {
			schema["required"] = t.InputSchema.Required
		}

		summary := t.Name
		if t.Annotations != nil && t.Annotations.Title != "" {
			summary = t.Annotations.Title
		}

		op := map[string]any{
			"operationId": t.Name,
			"summary":     summary,
			"description": t.Description,
			"requestBody": map[string]any{
				"required": len(t.InputSchema.Required) > 0,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": schema,
					},
				},
			},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "Successful tool response",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
							},
						},
					},
				},
			},
		}

		paths["/tools/"+t.Name] = map[string]any{
			"post": op,
		}
	}

	spec := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       s.name,
			"version":     s.version,
			"description": s.instructions,
		},
		"paths": paths,
	}
	if serverURL != "" {
		spec["servers"] = []map[string]any{
			{"url": serverURL},
		}
	}
	return spec
}

// ServeHTTP starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) ServeHTTP(ctx context.Context, cfg HTTPConfig) error {
	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: s.Handler(cfg),
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return nil
	case err := <-errCh:
		return err
	}
}

func withCORSAndAuth(next http.Handler, apiKey string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id, Accept")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if apiKey != "" {
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			expected := "Bearer " + apiKey
			if auth != expected && auth != apiKey {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized"})
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

