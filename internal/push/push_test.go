package push

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubAdapter is a simple test adapter that records Push calls.
type stubAdapter struct {
	harness string
	enabled bool
	calls   []stubCall
	err     error // error to return from Push
}

type stubCall struct {
	SessionID string
	AgentID   string
	Msg       Message
}

func (s *stubAdapter) HarnessType() string { return s.harness }
func (s *stubAdapter) Enabled() bool       { return s.enabled }
func (s *stubAdapter) Push(sessionID, agentID string, msg Message) error {
	s.calls = append(s.calls, stubCall{SessionID: sessionID, AgentID: agentID, Msg: msg})
	return s.err
}

func TestRegistryPushDispatches(t *testing.T) {
	r := NewRegistry()
	a := &stubAdapter{harness: "test", enabled: true}
	r.Register(a)

	msg := Message{ID: "m1", From: "ag-a", To: "ag-b", Body: "hello"}
	if err := r.Push("test", "sess-1", "ag-b", msg); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	if len(a.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(a.calls))
	}
	if a.calls[0].SessionID != "sess-1" {
		t.Fatalf("unexpected session id: %s", a.calls[0].SessionID)
	}
	if a.calls[0].Msg.Body != "hello" {
		t.Fatalf("unexpected body: %s", a.calls[0].Msg.Body)
	}
}

func TestRegistryPushUnknownHarness(t *testing.T) {
	r := NewRegistry()
	msg := Message{ID: "m1", From: "ag-a", To: "ag-b", Body: "hello"}
	err := r.Push("nonexistent", "sess-1", "ag-b", msg)
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	if !strings.Contains(err.Error(), "unknown harness type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegistryPushSkipsDisabled(t *testing.T) {
	r := NewRegistry()
	a := &stubAdapter{harness: "off", enabled: false}
	r.Register(a)

	msg := Message{ID: "m1", From: "ag-a", To: "ag-b", Body: "hello"}
	if err := r.Push("off", "sess-1", "ag-b", msg); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	if len(a.calls) != 0 {
		t.Fatal("expected no calls for disabled adapter")
	}
}

func TestRegistryPushAny(t *testing.T) {
	r := NewRegistry()
	a1 := &stubAdapter{harness: "h1", enabled: true}
	a2 := &stubAdapter{harness: "h2", enabled: false}
	a3 := &stubAdapter{harness: "h3", enabled: true}
	r.Register(a1)
	r.Register(a2)
	r.Register(a3)

	msg := Message{ID: "m1", From: "ag-a", To: "ag-b", Body: "hello"}
	if err := r.PushAny("sess-1", "ag-b", msg); err != nil {
		t.Fatalf("push any failed: %v", err)
	}
	if len(a1.calls) != 1 {
		t.Fatalf("expected 1 call on h1, got %d", len(a1.calls))
	}
	if len(a2.calls) != 0 {
		t.Fatal("expected no calls on disabled h2")
	}
	if len(a3.calls) != 1 {
		t.Fatalf("expected 1 call on h3, got %d", len(a3.calls))
	}
}

func TestRegistryPushAnyNoAdapters(t *testing.T) {
	r := NewRegistry()
	msg := Message{ID: "m1", From: "ag-a", To: "ag-b", Body: "hello"}
	if err := r.PushAny("sess-1", "ag-b", msg); err != nil {
		t.Fatalf("push any with no adapters should succeed, got: %v", err)
	}
}

func TestOpenCodeAdapterPush(t *testing.T) {
	var paths []string
	var bodies []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method == http.MethodGet && r.URL.Path == "/session/sess-1" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"directory":"/tmp/test"}`))
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

	a := NewOpenCodeAdapter(srv.URL, 5*time.Second, false)
	if a.HarnessType() != "opencode" {
		t.Fatalf("unexpected harness type: %s", a.HarnessType())
	}
	if !a.Enabled() {
		t.Fatal("expected adapter to be enabled")
	}

	msg := Message{ID: "msg-1", From: "ag-a", To: "ag-b", Body: "hello"}
	if err := a.Push("sess-1", "ag-b", msg); err != nil {
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

func TestOpenCodeAdapterDisabledOnEmptyURL(t *testing.T) {
	a := NewOpenCodeAdapter("", 5*time.Second, false)
	if a.Enabled() {
		t.Fatal("expected adapter to be disabled with empty URL")
	}
	msg := Message{ID: "m1", From: "ag-a", To: "ag-b", Body: "hello"}
	if err := a.Push("sess-1", "ag-b", msg); err != nil {
		t.Fatalf("disabled push should not error: %v", err)
	}
}

func TestOpenCodeAdapterErrorOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	a := NewOpenCodeAdapter(srv.URL, 5*time.Second, false)
	msg := Message{ID: "msg-1", From: "ag-a", To: "ag-b", Body: "hello"}
	err := a.Push("sess-1", "ag-b", msg)
	if err == nil {
		t.Fatal("expected push to fail for non-204 response")
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenCodeAdapterEmptySessionID(t *testing.T) {
	a := NewOpenCodeAdapter("http://localhost:1234", 5*time.Second, false)
	msg := Message{ID: "m1", From: "ag-a", To: "ag-b", Body: "hello"}
	err := a.Push("", "ag-b", msg)
	if err == nil {
		t.Fatal("expected error for empty session id")
	}
	if !strings.Contains(err.Error(), "session id is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
