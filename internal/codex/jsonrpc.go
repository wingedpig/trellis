// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// JSON-RPC 2.0 message types over newline-delimited JSON.
//
// The Codex app-server uses JSON-RPC 2.0 in both directions:
//
//   - We send Requests; it replies with Responses (matched by id).
//   - It sends Notifications (events like turn/started, item/started, etc.).
//   - It can send server-initiated Requests (e.g., command approval prompts);
//     we must reply with a Response.
//
// This client handles all four cases. It does not own the underlying io —
// callers pass in a Reader/Writer (typically a process's stdout/stdin).

const jsonrpcVersion = "2.0"

// rpcMessage is the union of all JSON-RPC frames we read from the server.
// Distinguishing a request, response, or notification requires inspecting
// which fields are present, so we decode loosely first.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// outgoing represents a frame to write. We encode under a write mutex.
type outgoing struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// NotificationHandler is called when the peer sends a notification (no id).
type NotificationHandler func(method string, params json.RawMessage)

// ServerRequestHandler is called when the peer sends a request (with id).
// The handler must return a result (any JSON-marshallable) or an *RPCError.
// Returning nil result + nil error sends a result of `null`.
type ServerRequestHandler func(method string, params json.RawMessage) (any, *RPCError)

// Client is a JSON-RPC 2.0 client over a single bidirectional stream.
// One Client per spawned codex app-server process.
//
// Lifecycle: NewClient → Run (in a goroutine) → Call/Notify/Close.
// Run blocks until the underlying reader closes; Close stops it.
type Client struct {
	r io.Reader
	w io.Writer

	wmu sync.Mutex // serialize writes (one frame at a time)
	enc *json.Encoder

	mu       sync.Mutex
	pending  map[string]chan rpcMessage // id (as string) -> reply channel
	nextID   atomic.Int64
	closed   bool
	closeErr error

	onNotification NotificationHandler
	onRequest      ServerRequestHandler
}

// NewClient wires up encoder/decoder and handlers.
func NewClient(r io.Reader, w io.Writer, onNotification NotificationHandler, onRequest ServerRequestHandler) *Client {
	c := &Client{
		r:              r,
		w:              w,
		enc:            json.NewEncoder(w),
		pending:        make(map[string]chan rpcMessage),
		onNotification: onNotification,
		onRequest:      onRequest,
	}
	c.enc.SetEscapeHTML(false)
	return c
}

// Run reads frames from r forever and dispatches them. Returns the io error
// that ended the loop (typically io.EOF).
//
// Notifications and server-requests are dispatched on per-frame goroutines so
// a slow handler never blocks reading. Responses are routed to the channel
// registered by Call.
func (c *Client) Run(ctx context.Context) error {
	// 16MB max line — Codex events with file diffs / command output can be large.
	br := bufio.NewReaderSize(c.r, 1024*1024)
	for {
		line, err := readLineRPC(br)
		if err != nil {
			c.fail(err)
			return err
		}
		if len(line) == 0 {
			continue
		}

		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Bad frame — log and keep going. A single malformed line shouldn't
			// kill the connection.
			continue
		}

		switch {
		case msg.Method != "" && msg.ID != nil:
			// Server-initiated request — reply asynchronously.
			go c.handleServerRequest(msg)
		case msg.Method != "":
			// Notification.
			if c.onNotification != nil {
				go c.onNotification(msg.Method, msg.Params)
			}
		case msg.ID != nil:
			// Response to one of our requests.
			c.deliverResponse(msg)
		default:
			// JSON-RPC frame with neither method nor id is invalid; drop.
		}
	}
}

func (c *Client) handleServerRequest(msg rpcMessage) {
	if c.onRequest == nil {
		// No handler — reply with "method not found" so the server isn't left waiting.
		_ = c.replyError(msg.ID, &RPCError{Code: -32601, Message: "method not found"})
		return
	}
	result, rpcErr := c.onRequest(msg.Method, msg.Params)
	if rpcErr != nil {
		_ = c.replyError(msg.ID, rpcErr)
		return
	}
	_ = c.replyResult(msg.ID, result)
}

func (c *Client) deliverResponse(msg rpcMessage) {
	// id can be a number or string in JSON-RPC; we stored it as the marshaled
	// form when we sent the request, so match on that string.
	idKey := string(*msg.ID)
	c.mu.Lock()
	ch, ok := c.pending[idKey]
	if ok {
		delete(c.pending, idKey)
	}
	c.mu.Unlock()
	if !ok {
		return // unmatched response, ignore
	}
	// Non-blocking send — the receiver always has a buffered channel of 1.
	select {
	case ch <- msg:
	default:
	}
}

// Call sends a request and waits for the matching response.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	idJSON, _ := json.Marshal(id)
	idKey := string(idJSON)

	ch := make(chan rpcMessage, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, c.closeErr
	}
	c.pending[idKey] = ch
	c.mu.Unlock()

	if err := c.write(outgoing{
		JSONRPC: jsonrpcVersion,
		ID:      idJSON,
		Method:  method,
		Params:  mustMarshal(params),
	}); err != nil {
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	}
}

// Notify sends a notification (no id, no response expected).
func (c *Client) Notify(method string, params any) error {
	return c.write(outgoing{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  mustMarshal(params),
	})
}

// replyResult sends a result response to a server-initiated request.
func (c *Client) replyResult(id *json.RawMessage, result any) error {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return c.replyError(id, &RPCError{Code: -32603, Message: "marshal result: " + err.Error()})
	}
	return c.write(outgoing{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(*id),
		Result:  resultBytes,
	})
}

// replyError sends an error response to a server-initiated request.
func (c *Client) replyError(id *json.RawMessage, e *RPCError) error {
	return c.write(outgoing{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(*id),
		Error:   e,
	})
}

func (c *Client) write(o outgoing) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return c.closeErr
	}
	return c.enc.Encode(o)
}

// fail marks the client as closed with a sticky error and unblocks all
// pending callers. Idempotent.
func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.closeErr = err
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
}

// Close shuts down the client. Safe to call multiple times.
func (c *Client) Close() {
	c.fail(io.EOF)
}

func mustMarshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		// Should not happen — params shapes are owned by us.
		return json.RawMessage("null")
	}
	return b
}

// readLineRPC reads one newline-terminated line from a *bufio.Reader. Mirrors
// the loop pattern used in internal/claude — bufio.Scanner can't handle very
// large frames (multi-MB tool results / file contents).
func readLineRPC(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			buf = append(buf, chunk...)
			continue
		}
		if err != nil {
			if len(buf) == 0 && len(chunk) == 0 {
				return nil, err
			}
			buf = append(buf, chunk...)
			return buf, err
		}
		buf = append(buf, chunk...)
		if n := len(buf); n > 0 && buf[n-1] == '\n' {
			buf = buf[:n-1]
			if n := len(buf); n > 0 && buf[n-1] == '\r' {
				buf = buf[:n-1]
			}
		}
		return buf, nil
	}
}
