// Package buffer talks to Buffer's public API (https://developers.buffer.com),
// with the owner's own API key from publish.buffer.com/settings/api. Every
// plan, Free included, may use it (Free: 3,000 requests in 30 days). MangoMan
// only lists the owner's channels and saves posts as drafts: nothing is
// published until the owner schedules it in Buffer. The API takes pictures
// only as public web links, which a file on the owner's computer is not, so
// the owner adds each picture to its draft in Buffer.
package buffer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// URL is Buffer's API (a test server in tests).
var URL = "https://api.buffer.com"

var client = &http.Client{Timeout: 30 * time.Second}

// Channel is one social account connected in Buffer.
type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service"` // instagram, facebook, linkedin, twitter, threads, ...
	Paused  bool   `json:"isQueuePaused"`
}

// call runs one GraphQL request and decodes data into out.
func call(ctx context.Context, key, query string, out any) error {
	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Buffer did not answer (%v)", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var r struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &r)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("Buffer refused the key: check it at publish.buffer.com/settings/api")
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("Buffer's request limit for this key is used up; try again in %s seconds", or(resp.Header.Get("Retry-After"), "a few hundred"))
	case len(r.Errors) > 0:
		return errors.New("Buffer: " + r.Errors[0].Message)
	case resp.StatusCode != http.StatusOK || len(r.Data) == 0:
		return fmt.Errorf("Buffer answered %s", resp.Status)
	}
	return json.Unmarshal(r.Data, out)
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// str is a GraphQL string literal; JSON's escapes are valid GraphQL ones.
func str(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Channels lists the channels of every organization the key can see.
func Channels(ctx context.Context, key string) ([]Channel, error) {
	var acc struct {
		Account struct {
			Organizations []struct{ ID string } `json:"organizations"`
		} `json:"account"`
	}
	if err := call(ctx, key, "query { account { organizations { id } } }", &acc); err != nil {
		return nil, err
	}
	out := []Channel{}
	for _, o := range acc.Account.Organizations {
		var c struct {
			Channels []Channel `json:"channels"`
		}
		q := fmt.Sprintf("query { channels(input: { organizationId: %s }) { id name service isQueuePaused } }", str(o.ID))
		if err := call(ctx, key, q, &c); err != nil {
			return nil, err
		}
		out = append(out, c.Channels...)
	}
	return out, nil
}

// Draft saves text as a draft on a channel, with its planned time when there
// is one, and returns the draft's id. A draft is never published by itself.
func Draft(ctx context.Context, key, channel, text string, due time.Time) (string, error) {
	when := "mode: addToQueue"
	if !due.IsZero() {
		when = "mode: customScheduled, dueAt: " + str(due.UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	q := fmt.Sprintf(`mutation { createPost(input: { text: %s, channelId: %s, schedulingType: automatic, %s, saveToDraft: true }) {
  ... on PostActionSuccess { post { id } }
  ... on MutationError { message } } }`, str(text), str(channel), when)
	var r struct {
		CreatePost struct {
			Post    *struct{ ID string } `json:"post"`
			Message string               `json:"message"`
		} `json:"createPost"`
	}
	if err := call(ctx, key, q, &r); err != nil {
		return "", err
	}
	if r.CreatePost.Post == nil {
		return "", errors.New("Buffer: " + strings.TrimSpace(or(r.CreatePost.Message, "the draft was not saved")))
	}
	return r.CreatePost.Post.ID, nil
}
