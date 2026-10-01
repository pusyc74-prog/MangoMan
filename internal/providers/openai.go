// Package providers talks to upstream model providers. Phase 1 needs one
// adapter: OpenAI-compatible Chat Completions, which every launch provider
// (Groq, Cerebras, OpenRouter, NVIDIA, Ollama) serves.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
)

// Version is sent in the User-Agent.
var Version = "dev"

// Client sends requests to OpenAI-compatible providers.
type Client struct {
	HTTP *http.Client
}

// NewClient returns a client with sane timeouts. There is no overall timeout,
// because streams can run for minutes; header and idle timeouts apply instead.
func NewClient() *Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
		ForceAttemptHTTP2:     true,
	}
	return &Client{HTTP: &http.Client{Transport: tr}}
}

func (c *Client) newRequest(ctx context.Context, p catalogue.Provider, key, method, path string, body []byte) (*http.Request, error) {
	url := strings.TrimRight(p.BaseURL, "/") + path
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("User-Agent", "MangoMan/"+Version)
	if p.ID == "openrouter" {
		req.Header.Set("X-Title", "MangoMan")
	}
	return req, nil
}

// Chat sends a Chat Completions request. The caller owns resp.Body.
func (c *Client) Chat(ctx context.Context, p catalogue.Provider, key string, body []byte, stream bool) (*http.Response, error) {
	req, err := c.newRequest(ctx, p, key, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return nil, err
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return c.HTTP.Do(req)
}

// ErrKeyRejected means the provider refused the key.
var ErrKeyRejected = errors.New("key rejected")

// ListModels returns the model ids a provider serves for this key.
func (c *Client) ListModels(ctx context.Context, p catalogue.Provider, key string) ([]string, error) {
	req, err := c.newRequest(ctx, p, key, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("%w by %s (HTTP %d)", ErrKeyRejected, p.Name, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("%s returned HTTP %d listing models", p.Name, resp.StatusCode)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("%s model list: %w", p.Name, err)
	}
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// ValidateKey checks a key by listing models.
func (c *Client) ValidateKey(ctx context.Context, p catalogue.Provider, key string) error {
	_, err := c.ListModels(ctx, p, key)
	return err
}

// DiscoverOllama lists local Ollama models. It returns nil, nil when Ollama is
// not running.
func (c *Client) DiscoverOllama(ctx context.Context, p catalogue.Provider) ([]catalogue.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := c.newRequest(ctx, p, "", http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil // not running
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list); err != nil {
		return nil, err
	}
	out := make([]catalogue.Model, 0, len(list.Data))
	for _, m := range list.Data {
		out = append(out, catalogue.Model{
			Canonical: m.ID, Provider: p.ID, Upstream: m.ID, Free: true, Context: 8192,
			Caps:    []string{"streaming", "json", "tools"},
			Quality: map[string]float64{"default": 0.45},
		})
	}
	return out, nil
}
