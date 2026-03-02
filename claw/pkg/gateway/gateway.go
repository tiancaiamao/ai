package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// JSONRPCRequest represents a JSON-RPC 2.0 request
type JSONRPCRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
	ID      interface{}            `json:"id,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// RPCError represents a JSON-RPC error
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Error codes
const (
	ErrorParseError     = -32700
	ErrorInvalidRequest = -32600
	ErrorMethodNotFound = -32601
	ErrorInvalidParams  = -32602
	ErrorInternalError  = -32603
)

// MethodHandler is a function that handles an RPC method
type MethodHandler func(params map[string]interface{}) (interface{}, error)

// MethodRegistry registers and calls RPC methods
type MethodRegistry struct {
	methods map[string]MethodHandler
	mu      sync.RWMutex
}

// NewMethodRegistry creates a new method registry
func NewMethodRegistry() *MethodRegistry {
	return &MethodRegistry{
		methods: make(map[string]MethodHandler),
	}
}

// Register registers a method handler
func (r *MethodRegistry) Register(method string, handler MethodHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods[method] = handler
}

// Call calls a method by name
func (r *MethodRegistry) Call(method string, params map[string]interface{}) (interface{}, error) {
	r.mu.RLock()
	handler, ok := r.methods[method]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("method not found: %s", method)
	}

	return handler(params)
}

// NewSuccessResponse creates a successful response
func NewSuccessResponse(id interface{}, result interface{}) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      id,
	}
}

// NewErrorResponse creates an error response
func NewErrorResponse(id interface{}, code int, message string) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
		ID: id,
	}
}

// Handler handles RPC requests
type Handler struct {
	registry *MethodRegistry
}

// NewHandler creates a new handler
func NewHandler() *Handler {
	return &Handler{
		registry: NewMethodRegistry(),
	}
}

// Register registers a method
func (h *Handler) Register(method string, handler MethodHandler) {
	h.registry.Register(method, handler)
}

// ServeHTTP handles HTTP requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		resp := NewErrorResponse(nil, ErrorParseError, "Parse error")
		h.writeResponse(w, resp)
		return
	}

	result, err := h.registry.Call(req.Method, req.Params)
	if err != nil {
		resp := NewErrorResponse(req.ID, ErrorInternalError, err.Error())
		h.writeResponse(w, resp)
		return
	}

	resp := NewSuccessResponse(req.ID, result)
	h.writeResponse(w, resp)
}

func (h *Handler) writeResponse(w http.ResponseWriter, resp *JSONRPCResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}