package buffer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A stand-in for Buffer's GraphQL API, shaped as its documentation shows.
func fake(t *testing.T, handle func(q string) (int, string)) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Unauthorized"}]}`))
			return
		}
		var in struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		code, body := handle(in.Query)
		if code == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "120")
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	old := URL
	URL = srv.URL
	t.Cleanup(func() { URL = old; srv.Close() })
}

func TestChannels(t *testing.T) {
	fake(t, func(q string) (int, string) {
		if strings.Contains(q, "organizations") {
			return 200, `{"data":{"account":{"organizations":[{"id":"org1"},{"id":"org2"}]}}}`
		}
		if strings.Contains(q, `organizationId: "org1"`) {
			return 200, `{"data":{"channels":[{"id":"c1","name":"kesarifoods","service":"instagram","isQueuePaused":false}]}}`
		}
		return 200, `{"data":{"channels":[{"id":"c2","name":"Kesari Foods","service":"linkedin","isQueuePaused":true}]}}`
	})
	ch, err := Channels(context.Background(), "good-key")
	if err != nil || len(ch) != 2 || ch[0].Service != "instagram" || !ch[1].Paused {
		t.Fatalf("channels: %+v %v", ch, err)
	}
	if _, err := Channels(context.Background(), "bad-key"); err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Fatalf("a wrong key: %v", err)
	}
}

func TestDraft(t *testing.T) {
	var got string
	fake(t, func(q string) (int, string) {
		got = q
		switch {
		case strings.Contains(q, `"full"`):
			return 200, `{"data":{"createPost":{"message":"Channel queue is full"}}}`
		case strings.Contains(q, `"busy"`):
			return 429, `{"errors":[{"message":"Too many requests from this client.","extensions":{"code":"RATE_LIMIT_EXCEEDED"}}]}`
		case strings.Contains(q, `"busy200"`): // Buffer's documented form: HTTP 200 with the code
			return 200, `{"errors":[{"message":"Too many requests","extensions":{"code":"RATE_LIMIT_EXCEEDED"}}]}`
		case strings.Contains(q, `"nokey"`):
			return 200, `{"errors":[{"message":"Not authorized","extensions":{"code":"UNAUTHORIZED"}}]}`
		case strings.Contains(q, `"nostatus"`):
			return 200, `{"data":{"createPost":{"post":{"id":"p8"}}}}`
		case strings.Contains(q, `"live"`):
			return 200, `{"data":{"createPost":{"post":{"id":"p9","status":"scheduled"}}}}`
		}
		return 200, `{"data":{"createPost":{"post":{"id":"p1","status":"draft"}}}}`
	})
	text := "Alphonso season is \"open\".\nOrder now #Mango"
	id, err := Draft(context.Background(), "good-key", "c1", text)
	if err != nil || id != "p1" {
		t.Fatalf("draft: %q %v", id, err)
	}
	for _, want := range []string{`text: "Alphonso season is \"open\".\nOrder now #Mango"`, `channelId: "c1"`, "saveToDraft: true",
		"mode: addToQueue", "schedulingType: automatic", "post { id status }"} {
		if !strings.Contains(got, want) {
			t.Errorf("the request lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "dueAt") || strings.Contains(got, "customScheduled") {
		t.Fatalf("a draft carries a time, a form Buffer does not document for drafts:\n%s", got)
	}
	if _, err := Draft(context.Background(), "good-key", "full", "x"); err == nil || !strings.Contains(err.Error(), "queue is full") {
		t.Fatalf("Buffer's own message: %v", err)
	}
	if _, err := Draft(context.Background(), "good-key", "busy", "x"); !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "120 seconds") {
		t.Fatalf("the limit says when to try again: %v", err)
	}
	if _, err := Draft(context.Background(), "good-key", "busy200", "x"); !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "a few minutes") {
		t.Fatalf("the limit as Buffer documents it: %v", err)
	}
	if _, err := Draft(context.Background(), "good-key", "nokey", "x"); !errors.Is(err, ErrKey) {
		t.Fatalf("a refused key as Buffer documents it: %v", err)
	}
	if id, err := Draft(context.Background(), "good-key", "live", "x"); !errors.Is(err, ErrNotDraft) || id != "p9" {
		t.Fatalf("a post Buffer did not keep as a draft must be flagged: %q %v", id, err)
	}
	if _, err := Draft(context.Background(), "good-key", "nostatus", "x"); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("a post with no status is not known to be a draft: %v", err)
	}
}
