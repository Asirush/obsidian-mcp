package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// ToolHandler is a function that executes an MCP tool.
type ToolHandler func(ctx context.Context, args map[string]any) CallToolResult

// ResourceListFunc returns available resources dynamically.
type ResourceListFunc func(ctx context.Context) ([]Resource, error)

// ResourceReadFunc reads the content of a resource by URI.
type ResourceReadFunc func(ctx context.Context, uri string) ([]ResourceContent, error)

// PromptHandler generates prompt messages for a given prompt call.
type PromptHandler func(ctx context.Context, args map[string]string) (*GetPromptResult, error)

type registeredTool struct {
	tool    Tool
	handler ToolHandler
}

type registeredPrompt struct {
	prompt  Prompt
	handler PromptHandler
}

// Server is a Model Context Protocol (MCP) JSON-RPC 2.0 server over stdio.
type Server struct {
	name              string
	version           string
	instructions      string
	mu                sync.RWMutex
	tools             []registeredTool
	toolMap           map[string]int
	resourceTemplates []ResourceTemplate
	resourceListFn    ResourceListFunc
	resourceReadFn    ResourceReadFunc
	prompts           []registeredPrompt
	promptMap         map[string]int
}

// NewServer creates a new MCP Server instance.
func NewServer(name, version, instructions string) *Server {
	return &Server{
		name:         name,
		version:      version,
		instructions: instructions,
		toolMap:      make(map[string]int),
		promptMap:    make(map[string]int),
	}
}

// RegisterTool registers a tool and its execution handler.
func (s *Server) RegisterTool(tool Tool, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx, exists := s.toolMap[tool.Name]; exists {
		s.tools[idx] = registeredTool{tool: tool, handler: handler}
		return
	}
	s.toolMap[tool.Name] = len(s.tools)
	s.tools = append(s.tools, registeredTool{tool: tool, handler: handler})
}

// SetResourceHandlers configures dynamic resource listing, templates, and reading.
func (s *Server) SetResourceHandlers(templates []ResourceTemplate, listFn ResourceListFunc, readFn ResourceReadFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resourceTemplates = templates
	s.resourceListFn = listFn
	s.resourceReadFn = readFn
}

// RegisterPrompt registers a prompt template and its handler.
func (s *Server) RegisterPrompt(prompt Prompt, handler PromptHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx, exists := s.promptMap[prompt.Name]; exists {
		s.prompts[idx] = registeredPrompt{prompt: prompt, handler: handler}
		return
	}
	s.promptMap[prompt.Name] = len(s.prompts)
	s.prompts = append(s.prompts, registeredPrompt{prompt: prompt, handler: handler})
}

// Serve runs the JSON-RPC loop reading from r and writing to w until EOF or ctx cancellation.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var writeMu sync.Mutex

	writeJSON := func(resp Response) error {
		data, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if _, err := w.Write(append(data, '\n')); err != nil {
			return err
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		payload, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if len(payload) == 0 {
			continue
		}

		resp := s.HandleMessage(ctx, payload)
		if resp != nil {
			if err := writeJSON(*resp); err != nil {
				return err
			}
		}
	}
}

// readMessage supports both newline-delimited JSON and Content-Length framed JSON-RPC messages.
func readMessage(r *bufio.Reader) ([]byte, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil && len(line) == 0 {
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if err != nil {
				return nil, err
			}
			continue
		}

		// Check if this is a Content-Length header
		if strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
			parts := strings.SplitN(trimmed, ":", 2)
			length, parseErr := strconv.Atoi(strings.TrimSpace(parts[1]))
			if parseErr != nil || length < 0 {
				return nil, fmt.Errorf("invalid Content-Length header: %q", trimmed)
			}
			// Consume remaining headers until empty line
			for {
				hdr, hdrErr := r.ReadString('\n')
				if hdrErr != nil {
					return nil, hdrErr
				}
				if strings.TrimSpace(hdr) == "" {
					break
				}
			}
			buf := make([]byte, length)
			if _, readErr := io.ReadFull(r, buf); readErr != nil {
				return nil, readErr
			}
			return buf, nil
		}

		return []byte(trimmed), nil
	}
}

// HandleMessage processes a single raw JSON-RPC message and returns a Response (or nil for notifications).
func (s *Server) HandleMessage(ctx context.Context, raw []byte) *Response {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage("null"),
			Error: &RPCError{
				Code:    ErrCodeParse,
				Message: "Parse error: " + err.Error(),
			},
		}
	}

	if req.JSONRPC != JSONRPCVersion && req.Method == "" {
		return &Response{
			JSONRPC: JSONRPCVersion,
			ID:      req.ID,
			Error: &RPCError{
				Code:    ErrCodeInvalidRequest,
				Message: "Invalid Request",
			},
		}
	}

	if req.IsNotification() {
		// Notifications do not receive a response.
		return nil
	}

	result, rpcErr := s.dispatch(ctx, req.Method, req.Params)
	if rpcErr != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			ID:      req.ID,
			Error:   rpcErr,
		}
	}

	return &Response{
		JSONRPC: JSONRPCVersion,
		ID:      req.ID,
		Result:  result,
	}
}

func (s *Server) dispatch(ctx context.Context, method string, rawParams json.RawMessage) (any, *RPCError) {
	switch method {
	case "initialize":
		var params InitializeParams
		if len(rawParams) > 0 {
			_ = json.Unmarshal(rawParams, &params)
		}
		protoVer := DefaultProtocolVer
		switch params.ProtocolVersion {
		case "2024-11-05", "2025-03-26", "2025-06-18":
			protoVer = params.ProtocolVersion
		default:
			if params.ProtocolVersion != "" {
				protoVer = params.ProtocolVersion
			}
		}

		s.mu.RLock()
		hasResources := s.resourceListFn != nil || s.resourceReadFn != nil || len(s.resourceTemplates) > 0
		hasPrompts := len(s.prompts) > 0
		s.mu.RUnlock()

		caps := ServerCapabilities{
			Tools: &ToolsCapability{},
		}
		if hasResources {
			caps.Resources = &ResourcesCapability{}
		}
		if hasPrompts {
			caps.Prompts = &PromptsCapability{}
		}

		return InitializeResult{
			ProtocolVersion: protoVer,
			Capabilities:    caps,
			ServerInfo: Implementation{
				Name:    s.name,
				Version: s.version,
			},
			Instructions: s.instructions,
		}, nil

	case "ping":
		return map[string]any{}, nil

	case "tools/list":
		s.mu.RLock()
		defer s.mu.RUnlock()
		tools := make([]Tool, len(s.tools))
		for i, rt := range s.tools {
			tools[i] = rt.tool
		}
		return map[string]any{"tools": tools}, nil

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return nil, &RPCError{
				Code:    ErrCodeInvalidParams,
				Message: "Invalid params: " + err.Error(),
			}
		}
		s.mu.RLock()
		idx, ok := s.toolMap[params.Name]
		var handler ToolHandler
		if ok {
			handler = s.tools[idx].handler
		}
		s.mu.RUnlock()

		if !ok || handler == nil {
			return nil, &RPCError{
				Code:    ErrCodeMethodNotFound,
				Message: fmt.Sprintf("Unknown tool: %s", params.Name),
			}
		}
		if params.Arguments == nil {
			params.Arguments = make(map[string]any)
		}
		res := handler(ctx, params.Arguments)
		return res, nil

	case "resources/list":
		s.mu.RLock()
		listFn := s.resourceListFn
		s.mu.RUnlock()
		if listFn == nil {
			return map[string]any{"resources": []Resource{}}, nil
		}
		resources, err := listFn(ctx)
		if err != nil {
			return nil, &RPCError{
				Code:    ErrCodeInternal,
				Message: err.Error(),
			}
		}
		if resources == nil {
			resources = []Resource{}
		}
		return map[string]any{"resources": resources}, nil

	case "resources/templates/list":
		s.mu.RLock()
		templates := s.resourceTemplates
		s.mu.RUnlock()
		if templates == nil {
			templates = []ResourceTemplate{}
		}
		return map[string]any{"resourceTemplates": templates}, nil

	case "resources/read":
		var params ReadResourceParams
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return nil, &RPCError{
				Code:    ErrCodeInvalidParams,
				Message: "Invalid params: " + err.Error(),
			}
		}
		s.mu.RLock()
		readFn := s.resourceReadFn
		s.mu.RUnlock()
		if readFn == nil {
			return nil, &RPCError{
				Code:    ErrCodeMethodNotFound,
				Message: "Resources not supported",
			}
		}
		contents, err := readFn(ctx, params.URI)
		if err != nil {
			return nil, &RPCError{
				Code:    ErrCodeInvalidParams,
				Message: err.Error(),
			}
		}
		return map[string]any{"contents": contents}, nil

	case "prompts/list":
		s.mu.RLock()
		defer s.mu.RUnlock()
		prompts := make([]Prompt, len(s.prompts))
		for i, rp := range s.prompts {
			prompts[i] = rp.prompt
		}
		return map[string]any{"prompts": prompts}, nil

	case "prompts/get":
		var params GetPromptParams
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return nil, &RPCError{
				Code:    ErrCodeInvalidParams,
				Message: "Invalid params: " + err.Error(),
			}
		}
		s.mu.RLock()
		idx, ok := s.promptMap[params.Name]
		var handler PromptHandler
		if ok {
			handler = s.prompts[idx].handler
		}
		s.mu.RUnlock()

		if !ok || handler == nil {
			return nil, &RPCError{
				Code:    ErrCodeMethodNotFound,
				Message: fmt.Sprintf("Unknown prompt: %s", params.Name),
			}
		}
		res, err := handler(ctx, params.Arguments)
		if err != nil {
			return nil, &RPCError{
				Code:    ErrCodeInvalidParams,
				Message: err.Error(),
			}
		}
		return res, nil

	default:
		return nil, &RPCError{
			Code:    ErrCodeMethodNotFound,
			Message: fmt.Sprintf("Method not found: %s", method),
		}
	}
}

// Helper functions for extracting typed tool arguments safely.

func GetString(args map[string]any, key string, defaultVal string) string {
	val, ok := args[key]
	if !ok || val == nil {
		return defaultVal
	}
	if s, ok := val.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", val)
}

func GetBool(args map[string]any, key string, defaultVal bool) bool {
	val, ok := args[key]
	if !ok || val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return defaultVal
}

func GetInt(args map[string]any, key string, defaultVal int) int {
	val, ok := args[key]
	if !ok || val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		i, err := strconv.Atoi(v)
		if err == nil {
			return i
		}
	}
	return defaultVal
}

func GetStringSlice(args map[string]any, key string) []string {
	val, ok := args[key]
	if !ok || val == nil {
		return nil
	}
	switch v := val.(type) {
	case []string:
		return v
	case []any:
		res := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				res = append(res, s)
			} else if item != nil {
				res = append(res, fmt.Sprintf("%v", item))
			}
		}
		return res
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return nil
}

func GetMap(args map[string]any, key string) map[string]any {
	val, ok := args[key]
	if !ok || val == nil {
		return nil
	}
	if m, ok := val.(map[string]any); ok {
		return m
	}
	return nil
}

func BoolPtr(b bool) *bool {
	return &b
}

