// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

// Package rpc serves the JSON-RPC 2.0 contract (docs/jsonrpc-contract.md)
// over the loopback WebSocket. One contract, three runtimes: the PWA, this
// Go daemon, and the Android companion.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const writeTimeout = 5 * time.Second

// JSON-RPC 2.0 error codes used by the daemon.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeServerError    = -32000
)

// Handler executes one method and returns the result payload.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Handlers maps method names to their implementations.
type Handlers map[string]Handler

// Broker owns all live PWA connections: it serves each connection and
// broadcasts daemon-initiated notifications.
type Broker struct {
	mu    sync.Mutex
	conns map[*websocket.Conn]struct{}
}

// NewBroker creates an empty broker.
func NewBroker() *Broker {
	return &Broker{conns: map[*websocket.Conn]struct{}{}}
}

// Broadcast sends a notification to every connected client (best effort).
func (b *Broker) Broadcast(method string, params any) {
	message, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for conn := range b.conns {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err := conn.Write(ctx, websocket.MessageText, message)
		cancel()
		if err != nil {
			log.Printf("rpc: broadcast to %p failed: %v", conn, err)
		}
	}
}

// ServeConn handles one WebSocket connection until the client disconnects.
// Requests are dispatched serially per connection (the daemon's methods are
// fast; heavy work belongs in the queue).
func (b *Broker) ServeConn(ctx context.Context, handler func(ctx context.Context, request Request) (any, *RPCError), conn *websocket.Conn) {
	b.mu.Lock()
	b.conns[conn] = struct{}{}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.conns, conn)
		b.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return // normal end: client closed or ctx cancelled
		}
		var request Request
		if err := json.Unmarshal(data, &request); err != nil {
			b.writeError(ctx, conn, nil, &RPCError{Code: CodeParseError, Message: "request is not valid JSON"})
			continue
		}
		if err := validate(&request); err != nil {
			b.writeError(ctx, conn, request.ID, err)
			continue
		}
		result, rpcErr := handler(ctx, request)
		if rpcErr != nil {
			b.writeError(ctx, conn, request.ID, rpcErr)
			continue
		}
		// Notifications (no id) get no response.
		if request.ID == nil {
			continue
		}
		b.writeResult(ctx, conn, *request.ID, result)
	}
}

func (b *Broker) writeResult(ctx context.Context, conn *websocket.Conn, id RequestID, result any) {
	response, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		log.Printf("rpc: encode response: %v", err)
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := conn.Write(writeCtx, websocket.MessageText, response); err != nil {
		log.Printf("rpc: write response: %v", err)
	}
}

func (b *Broker) writeError(ctx context.Context, conn *websocket.Conn, id *RequestID, rpcErr *RPCError) {
	if id == nil {
		id = &RequestID{}
	}
	response, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": rpcErr.Code, "message": rpcErr.Message},
	})
	if err != nil {
		log.Printf("rpc: encode error response: %v", err)
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := conn.Write(writeCtx, websocket.MessageText, response); err != nil {
		log.Printf("rpc: write error response: %v", err)
	}
}

// RequestID is a JSON-RPC id: number or string.
type RequestID struct {
	value any
}

// UnmarshalJSON accepts numbers and strings, rejecting anything else.
func (r *RequestID) UnmarshalJSON(data []byte) error {
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		r.value = n
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		r.value = s
		return nil
	}
	return fmt.Errorf("id must be a number or string")
}

// MarshalJSON emits the raw id value.
func (r RequestID) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.value)
}

// Request is an incoming JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *RequestID      `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int
	Message string
}

// Error lets handlers return *RPCError as a plain error.
func (e *RPCError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

func validate(request *Request) *RPCError {
	if request.JSONRPC != "2.0" {
		return &RPCError{Code: CodeInvalidRequest, Message: `jsonrpc must be exactly "2.0"`}
	}
	if request.Method == "" {
		return &RPCError{Code: CodeInvalidRequest, Message: "method is required"}
	}
	return nil
}

// ParseParams decodes params into a typed value, mapping failures to -32602.
// Unknown fields are rejected: the contract is strict by design (spec §6).
func ParseParams(params json.RawMessage, target any) *RPCError {
	if len(params) == 0 {
		return &RPCError{Code: CodeInvalidParams, Message: "params are required"}
	}
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &RPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("invalid params: %v", err)}
	}
	return nil
}
