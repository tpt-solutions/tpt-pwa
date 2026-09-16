// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func startServer(t *testing.T, handlers Handlers) (*Broker, *websocket.Conn) {
	t.Helper()
	broker := NewBroker()
	dispatch := func(_ context.Context, request Request) (any, *RPCError) {
		handler, ok := handlers[request.Method]
		if !ok {
			return nil, &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + request.Method}
		}
		result, err := handler(context.Background(), request.Params)
		if err != nil {
			if rpcErr, ok := err.(*RPCError); ok {
				return nil, rpcErr
			}
			return nil, &RPCError{Code: CodeServerError, Message: err.Error()}
		}
		return result, nil
	}
	server := httptest.NewServer(broker.adapt(dispatch))
	t.Cleanup(server.Close)
	conn, _, err := websocket.Dial(context.Background(), "ws://"+strings.TrimPrefix(server.URL, "http://")+"/rpc", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return broker, conn
}

// adapt turns the broker into an http.Handler for tests.
func (b *Broker) adapt(dispatch func(context.Context, Request) (any, *RPCError)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		b.ServeConn(r.Context(), dispatch, conn)
	}
}

func roundtrip(t *testing.T, conn *websocket.Conn, payload string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode response %s: %v", data, err)
	}
	return response
}

func TestPingRoundtrip(t *testing.T) {
	_, conn := startServer(t, Handlers{
		"cortex.ping": func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]any{"pong": true}, nil
		},
	})
	response := roundtrip(t, conn, `{"jsonrpc":"2.0","id":1,"method":"cortex.ping"}`)
	if response["result"].(map[string]any)["pong"] != true {
		t.Fatalf("unexpected response: %v", response)
	}
}

func TestMethodNotFound(t *testing.T) {
	_, conn := startServer(t, Handlers{})
	response := roundtrip(t, conn, `{"jsonrpc":"2.0","id":2,"method":"cortex.doesNotExist"}`)
	errObj := response["error"].(map[string]any)
	if errObj["code"].(float64) != CodeMethodNotFound {
		t.Fatalf("expected -32601, got %v", errObj)
	}
}

func TestMalformedJSONGetsParseError(t *testing.T) {
	_, conn := startServer(t, Handlers{})
	response := roundtrip(t, conn, `{not json`)
	errObj := response["error"].(map[string]any)
	if errObj["code"].(float64) != CodeParseError {
		t.Fatalf("expected -32700, got %v", errObj)
	}
}

func TestInvalidParams(t *testing.T) {
	_, conn := startServer(t, Handlers{
		"cortex.task.enqueue": func(_ context.Context, params json.RawMessage) (any, error) {
			var p struct {
				Kind string `json:"kind"`
			}
			if err := ParseParams(params, &p); err != nil {
				return nil, err
			}
			return map[string]any{"kind": p.Kind}, nil
		},
	})
	response := roundtrip(t, conn, `{"jsonrpc":"2.0","id":3,"method":"cortex.task.enqueue","params":{"payload":{}}}`)
	if response["error"].(map[string]any)["code"].(float64) != CodeInvalidParams {
		t.Fatalf("expected -32602, got %v", response)
	}
}

func TestBroadcastReachesClient(t *testing.T) {
	broker, conn := startServer(t, Handlers{})
	broker.Broadcast("cortex.event.taskCompleted", map[string]any{"taskId": "abc", "state": "completed"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read notification: %v", err)
	}
	var notification map[string]any
	if err := json.Unmarshal(data, &notification); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if notification["method"] != "cortex.event.taskCompleted" {
		t.Fatalf("unexpected notification: %v", notification)
	}
}
