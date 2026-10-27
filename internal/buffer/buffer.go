// Package buffer talks to Buffer's public API (https://developers.buffer.com),
// with the owner's own API key from publish.buffer.com/settings/api. Every
// plan, Free included, may use it (100 requests in 15 minutes, 250 a day,
// 3,000 in 30 days). MangoMan only lists the owner's channels and saves
// posts as drafts, in the form Buffer documents (mode addToQueue with
// saveToDraft): nothing is published until the owner schedules it in
// Buffer. A draft carries no time, and the API takes pictures only as public
// web links, which a file on the owner's computer is not, so the owner adds
// each picture and time in Buffer.
package buffer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// URL is Buffer's API (a test server in tests).
var URL = "https://api.buffer.com"

var client = &http.Client{Timeout: 30 * time.Second}

// ErrKey and ErrLimit are Buffer refusing the key and the key's request
// limit being used up; nothing more should be sent after either.
var (
	ErrKey   = errors.New("Buffer refused the key: check it at publish.buffer.com/settings/api")
	ErrLimit = errors.New("Buffer's request limit for this key is used up")
)

// Channel is one social account connected in Buffer.
type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service"` // instagram, facebook, linkedin, twitter, threads, ...
	Paused  bool   `json:"isQueuePaused"`
}

// call runs one GraphQL request and decodes data into out. Buffer answers
// HTTP 200 with an error code for a bad key or the limit; the HTTP codes
// are read too.
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
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("Buffer's answer was cut off (%v)", err)
	}
	var r struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &r)
	code := ""
	if len(r.Errors) > 0 {
		code = r.Errors[0].Extensions.Code
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || code == "UNAUTHORIZED" || code == "FORBIDDEN":
		return ErrKey
	case resp.StatusCode == http.StatusTooManyRequests || code == "RATE_LIMIT_EXCEEDED":
		wait := "a few minutes"
		if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n > 0 {
			wait = strconv.Itoa(n) + " seconds"
		}
		return fmt.Errorf("%w; try again in %s", ErrLimit, wait)
	case len(r.Errors) > 0:
		return errors.New("Buffer: " + r.Errors[0].Message)
	case resp.StatusCode != http.StatusOK || len(r.Data) == 0:
		return fmt.Errorf("Buffer answered %s", resp.Status)
	}
	return json.Unmarshal(r.Data, out)
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

// Draft saves text as a draft on a channel and returns the draft's id. A
// post Buffer reports as anything but a draft is an error, so the owner
// checks it at once.
func Draft(ctx context.Context, key, channel, text string) (string, error) {
	q := fmt.Sprintf(`mutation { createPost(input: { text: %s, channelId: %s, schedulingType: automatic, mode: addToQueue, saveToDraft: true }) {
  ... on PostActionSuccess { post { id status } }
  ... on MutationError { message } } }`, str(text), str(channel))
	var r struct {
		CreatePost struct {
			Post *struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"post"`
			Message string `json:"message"`
		} `json:"createPost"`
	}
	if err := call(ctx, key, q, &r); err != nil {
		return "", err
	}
	switch p := r.CreatePost.Post; {
	case p == nil:
		msg := strings.TrimSpace(r.CreatePost.Message)
		if msg == "" {
			msg = "the draft was not saved"
		}
		return "", errors.New("Buffer: " + msg)
	case p.Status != "" && !strings.EqualFold(p.Status, "draft"):
		return p.ID, fmt.Errorf("Buffer saved this post as %q, not as a draft: open Buffer and check it now", p.Status)
	}
	return r.CreatePost.Post.ID, nil
}
