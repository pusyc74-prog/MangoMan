// Package mcp is MangoMan's assist mode: a Model Context Protocol server
// (JSON-RPC 2.0 over stdio) that a paid main model, such as Claude in Claude
// Code or GPT in Codex, calls to hand routine work to free models. Requests
// go to the locally running router, so quota, failover and the quality guard
// apply as usual.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Tool is one tool the server offers.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Call runs the tool. A returned error becomes a tool error the model
	// sees (isError), not a protocol error.
	Call func(ctx context.Context, args json.RawMessage) (string, error) `json:"-"`
}

// Server serves tools over a stream of newline-delimited JSON-RPC messages.
type Server struct {
	Name         string
	Version      string
	Instructions string
	Tools        []Tool

	mu  sync.Mutex // serialises writes
	out io.Writer
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Protocol versions this server speaks; the client's is echoed if listed.
var supported = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// Serve reads requests from in and writes responses to out until in ends
// or ctx is cancelled. Tool calls run concurrently.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = out
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var wg sync.WaitGroup
	defer wg.Wait()
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if line[0] == '[' {
			// Batches were dropped from MCP in 2025-06-18; answer each item.
			var batch []json.RawMessage
			if json.Unmarshal(line, &batch) != nil {
				s.reply(nil, nil, &rpcError{-32700, "parse error"})
				continue
			}
			for _, b := range batch {
				s.handle(ctx, b, &wg)
			}
			continue
		}
		s.handle(ctx, append([]byte(nil), line...), &wg)
	}
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, data []byte, wg *sync.WaitGroup) {
	var m rpcMsg
	if err := json.Unmarshal(data, &m); err != nil {
		s.reply(nil, nil, &rpcError{-32700, "parse error"})
		return
	}
	isRequest := len(m.ID) > 0 && string(m.ID) != "null"
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		v := supported[0]
		for _, sv := range supported {
			if sv == p.ProtocolVersion {
				v = sv
			}
		}
		s.reply(m.ID, map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    s.Instructions,
		}, nil)
	case "ping":
		s.reply(m.ID, map[string]any{}, nil)
	case "tools/list":
		s.reply(m.ID, map[string]any{"tools": s.Tools}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			s.reply(m.ID, nil, &rpcError{-32602, "invalid params"})
			return
		}
		var tool *Tool
		for i := range s.Tools {
			if s.Tools[i].Name == p.Name {
				tool = &s.Tools[i]
			}
		}
		if tool == nil {
			s.reply(m.ID, nil, &rpcError{-32602, "unknown tool: " + p.Name})
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			args := p.Arguments
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			text, err := tool.Call(ctx, args)
			res := map[string]any{"isError": err != nil}
			if err != nil {
				text = err.Error()
			}
			res["content"] = []map[string]any{{"type": "text", "text": text}}
			s.reply(m.ID, res, nil)
		}()
	default:
		if isRequest {
			s.reply(m.ID, nil, &rpcError{-32601, "method not found: " + m.Method})
		}
		// Notifications (initialized, cancelled, ...) need no answer.
	}
}

func (s *Server) reply(id json.RawMessage, result any, e *rpcError) {
	if id == nil {
		id = json.RawMessage("null")
	}
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		msg["error"] = e
	} else {
		msg["result"] = result
	}
	b, err := json.Marshal(msg)
	if err != nil {
		b = []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"internal error"}}`, id))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}
