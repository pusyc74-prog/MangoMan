package core

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// WriteError writes an OpenAI-style error body.
func WriteError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": msg, "type": typ, "code": typ},
	})
}

// WriteExhausted writes a 429 telling the client when free capacity returns.
func WriteExhausted(w http.ResponseWriter, retryAt time.Time, msg string) {
	secs := int(time.Until(retryAt).Seconds()) + 1
	if retryAt.IsZero() || secs < 1 {
		secs = 60
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	WriteError(w, http.StatusTooManyRequests, "free_capacity_exhausted", msg)
}
