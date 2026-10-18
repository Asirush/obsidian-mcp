package mcp

import "encoding/json"

const (
	JSONRPCVersion        = "2.0"
	DefaultProtocolVer    = "2024-11-05"
	LatestProtocolVer     = "2025-03-26"
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
)

// Request represents a JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification returns true if the request has no ID.
func (r *Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// InitializeParams represents the parameters sent by the client during "initialize".
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	ClientInfo      Implementation `json:"clientInfo"`
}

// Implementation describes the name and version of an MCP endpoint.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is returned by the server for "initialize".
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// ServerCapabilities declares what features the MCP server supports.
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// Tool represents an MCP tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema InputSchema     `json:"inputSchema"`
	Annotations *ToolAnnotation `json:"annotations,omitempty"`
}

// ToolAnnotation provides optional hints about tool behavior.
type ToolAnnotation struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
}

// InputSchema defines the JSON Schema for a tool's input parameters.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// Property represents a single JSON Schema property definition.
type Property struct {
	Type                 string              `json:"type"`
	Description          string              `json:"description,omitempty"`
	Enum                 []string            `json:"enum,omitempty"`
	Default              any                 `json:"default,omitempty"`
	Items                *Property           `json:"items,omitempty"`
	Properties           map[string]Property `json:"properties,omitempty"`
	AdditionalProperties any                 `json:"additionalProperties,omitempty"`
}

// CallToolParams represents the arguments to "tools/call".
type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolResult represents the result of "tools/call".
type CallToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem represents a piece of content returned by a tool or prompt.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Resource represents an MCP resource entry.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourceTemplate represents a parameterized MCP resource URI template.
type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ReadResourceParams represents the parameters for "resources/read".
type ReadResourceParams struct {
	URI string `json:"uri"`
}

// ResourceContent represents the content of a read resource.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// Prompt represents an MCP prompt definition.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument describes an argument accepted by a prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// GetPromptParams represents parameters for "prompts/get".
type GetPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// GetPromptResult represents the result of "prompts/get".
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptMessage represents a message in a prompt template.
type PromptMessage struct {
	Role    string      `json:"role"`
	Content ContentItem `json:"content"`
}

// NewTextResult creates a successful CallToolResult with text content.
func NewTextResult(text string) CallToolResult {
	return CallToolResult{
		Content: []ContentItem{
			{Type: "text", Text: text},
		},
	}
}

// NewJSONResult serializes v as indented JSON and wraps it in a CallToolResult.
func NewJSONResult(v any) CallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return NewErrorResult("failed to serialize result: " + err.Error())
	}
	return NewTextResult(string(data))
}

// NewErrorResult creates an error CallToolResult with text content.
func NewErrorResult(errMsg string) CallToolResult {
	return CallToolResult{
		Content: []ContentItem{
			{Type: "text", Text: errMsg},
		},
		IsError: true,
	}
}

