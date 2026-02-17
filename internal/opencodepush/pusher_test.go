package opencodepush

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tanwa/relay-mesh/internal/broker"
)

func TestPushPostsPromptAsync(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := New(srv.URL, 5*time.Second, false)
	msg := broker.Message{ID: "msg-1", From: "ag-a", To: "ag-b", Body: "hello"}
	if err := p.Push("sess-1", "ag-b", msg); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	if gotPath != "/session/sess-1/prompt_async" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	parts, ok := gotBody["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("unexpected parts payload: %#v", gotBody["parts"])
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected part payload: %#v", parts[0])
	}
	text, _ := part["text"].(string)
	if !strings.Contains(text, "hello") || !strings.Contains(text, "msg-1") {
		t.Fatalf("missing expected text fields in payload: %q", text)
	}
}

func TestPushReturnsErrorOnNon204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	p := New(srv.URL, 5*time.Second, false)
	msg := broker.Message{ID: "msg-1", From: "ag-a", To: "ag-b", Body: "hello"}
	err := p.Push("sess-1", "ag-b", msg)
	if err == nil {
		t.Fatal("expected push to fail for non-204 response")
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("unexpected error: %v", err)
	}
}

