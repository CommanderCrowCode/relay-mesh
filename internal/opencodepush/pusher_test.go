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
	var paths []string
	var bodies []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method == http.MethodGet && r.URL.Path == "/session/sess-1" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"directory":"/Users/tanwa/playground/relay_test"}`))
			return
		}
		gotBody := map[string]any{}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		bodies = append(bodies, gotBody)
		if r.URL.Path == "/tui/show-toast" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := New(srv.URL, 5*time.Second, false)
	msg := broker.Message{ID: "msg-1", From: "ag-a", To: "ag-b", Body: "hello"}
	if err := p.Push("sess-1", "ag-b", msg); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	if len(paths) < 3 {
		t.Fatalf("expected at least 3 calls, got %d", len(paths))
	}
	if paths[0] != "/session/sess-1/prompt_async" {
		t.Fatalf("unexpected first path: %s", paths[0])
	}
	if paths[1] != "/session/sess-1" {
		t.Fatalf("unexpected second path: %s", paths[1])
	}
	if paths[2] != "/tui/show-toast" {
		t.Fatalf("unexpected third path: %s", paths[2])
	}
	parts, ok := bodies[0]["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("unexpected parts payload: %#v", bodies[0]["parts"])
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
