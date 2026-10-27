package buffer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		if strings.Contains(q, `"full"`) {
			return 200, `{"data":{"createPost":{"message":"Channel queue is full"}}}`
		}
		if strings.Contains(q, `"busy"`) {
			return 429, `{"errors":[{"message":"Too many requests from this client.","extensions":{"code":"RATE_LIMIT_EXCEEDED"}}]}`
		}
		return 200, `{"data":{"createPost":{"post":{"id":"p1"}}}}`
	})
	text := "Alphonso season is \"open\".\nOrder now #Mango"
	due := time.Date(2026, 11, 2, 18, 30, 0, 0, time.FixedZone("IST", 5*3600+1800))
	id, err := Draft(context.Background(), "good-key", "c1", text, due)
	if err != nil || id != "p1" {
		t.Fatalf("draft: %q %v", id, err)
	}
	for _, want := range []string{`text: "Alphonso season is \"open\".\nOrder now #Mango"`, `channelId: "c1"`, "saveToDraft: true",
		"mode: customScheduled", `dueAt: "2026-11-02T13:00:00.000Z"`, "schedulingType: automatic"} {
		if !strings.Contains(got, want) {
			t.Errorf("the request lacks %s:\n%s", want, got)
		}
	}
	if _, err := Draft(context.Background(), "good-key", "c1", "no time", time.Time{}); err != nil || !strings.Contains(got, "mode: addToQueue") || strings.Contains(got, "dueAt") {
		t.Fatalf("a draft without a time goes to the queue: %v\n%s", err, got)
	}
	if _, err := Draft(context.Background(), "good-key", "full", "x", time.Time{}); err == nil || !strings.Contains(err.Error(), "queue is full") {
		t.Fatalf("Buffer's own message: %v", err)
	}
	if _, err := Draft(context.Background(), "good-key", "busy", "x", time.Time{}); err == nil || !strings.Contains(err.Error(), "120 seconds") {
		t.Fatalf("the limit says when to try again: %v", err)
	}
}
