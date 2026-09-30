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
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	writeTimeout = 5 * time.Second
	pingEvery    = 30 * time.Second
	pingTimeout  = 10 * time.Second
)

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
	conns map[*websocket.Conn]*connState
}

// connState serializes writes to one connection: websocket writes must not
// interleave (data frames, error frames, pings), and no writer may ever hold
// the broker-wide lock while writing -- one slow client must not stall the
// others or the read loops.
type connState struct {
	writeMu sync.Mutex
}

// NewBroker creates an empty broker and starts its keepalive loop.
func NewBroker() *Broker {
	b := &Broker{conns: map[*websocket.Conn]*connState{}}
	go b.keepalive()
	return b
}

// keepalive pings every connected client. A client that stops answering is
// closed so its slot (and the PWA's reconnect logic) recovers promptly
// instead of lurking half-open for hours.
func (b *Broker) keepalive() {
	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()
	for range ticker.C {
		b.mu.Lock()
		snapshot := make([]*websocket.Conn, 0, len(b.conns))
		for conn := range b.conns {
			snapshot = append(snapshot, conn)
		}
		b.mu.Unlock()
		for _, conn := range snapshot {
			state, ok := b.stateFor(conn)
			if !ok {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
			state.writeMu.Lock()
			err := conn.Ping(ctx)
			state.writeMu.Unlock()
			cancel()
			if err != nil {
				slog.Warn("keepalive ping failed", "component", "rpc", "conn", fmt.Sprintf("%p", conn), "error", err)
				conn.Close(websocket.StatusGoingAway, "keepalive timeout")
			}
		}
	}
}

func (b *Broker) stateFor(conn *websocket.Conn) (*connState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.conns[conn]
	return state, ok
}

// Broadcast sends a notification to every connected client (best effort).
// Writes happen outside the broker lock through each connection's own write
// mutex, so a stalled client delays only its own delivery.
func (b *Broker) Broadcast(method string, params any) {
	message, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return
	}
	b.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(b.conns))
	for conn := range b.conns {
		conns = append(conns, conn)
	}
	b.mu.Unlock()
	for _, conn := range conns {
		state, ok := b.stateFor(conn)
		if !ok {
			continue // gone between snapshot and write
		}
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		state.writeMu.Lock()
		err := conn.Write(ctx, websocket.MessageText, message)
		state.writeMu.Unlock()
		cancel()
		if err != nil {
			slog.Warn("broadcast failed", "component", "rpc", "conn", fmt.Sprintf("%p", conn), "error", err)
		}
	}
}

// ServeConn handles one WebSocket connection until the client disconnects.
// Requests are dispatched serially per connection (the daemon's methods are
// fast; heavy work belongs in the queue).
func (b *Broker) ServeConn(ctx context.Context, handler func(ctx context.Context, request Request) (any, *RPCError), conn *websocket.Conn) {
	state := &connState{}
	b.mu.Lock()
	b.conns[conn] = state
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

// write marshals and sends one frame under the connection's write mutex.
func (b *Broker) write(ctx context.Context, conn *websocket.Conn, payload []byte) error {
	state, ok := b.stateFor(conn)
	if !ok {
		state = &connState{} // connection already left the broker; still serialize locally
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	state.writeMu.Lock()
	defer state.writeMu.Unlock()
	return conn.Write(writeCtx, websocket.MessageText, payload)
}

func (b *Broker) writeResult(ctx context.Context, conn *websocket.Conn, id RequestID, result any) {
	response, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		slog.Error("encode response failed", "component", "rpc", "error", err)
		return
	}
	if err := b.write(ctx, conn, response); err != nil {
		slog.Warn("write response failed", "component", "rpc", "error", err)
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
		slog.Error("encode error response failed", "component", "rpc", "error", err)
		return
	}
	if err := b.write(ctx, conn, response); err != nil {
		slog.Warn("write error response failed", "component", "rpc", "error", err)
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
