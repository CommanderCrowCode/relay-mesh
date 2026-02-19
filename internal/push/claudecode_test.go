package push

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeCodeHarnessType(t *testing.T) {
	a := NewClaudeCodeAdapter(t.TempDir())
	if got := a.HarnessType(); got != "claude-code" {
		t.Fatalf("expected harness type 'claude-code', got %q", got)
	}
}

func TestClaudeCodeEnabled(t *testing.T) {
	a := NewClaudeCodeAdapter(t.TempDir())
	if !a.Enabled() {
		t.Fatal("expected adapter to always be enabled")
	}
}

func TestClaudeCodePushWritesStateFile(t *testing.T) {
	dir := t.TempDir()
	a := NewClaudeCodeAdapter(dir)

	msg := Message{ID: "msg-1", From: "ag-a", To: "ag-b", Body: "hello world", CreatedAt: "2026-02-18T10:00:00Z"}
	if err := a.Push("sess-1", "ag-b", msg); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	stateFile := filepath.Join(dir, "pending-messages.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}

	var pending []pendingMessage
	if err := json.Unmarshal(data, &pending); err != nil {
		t.Fatalf("unmarshal state file: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending message, got %d", len(pending))
	}
	if pending[0].From != "ag-a" {
		t.Fatalf("expected from 'ag-a', got %q", pending[0].From)
	}
	if pending[0].Body != "hello world" {
		t.Fatalf("expected body 'hello world', got %q", pending[0].Body)
	}
	if pending[0].MessageID != "msg-1" {
		t.Fatalf("expected message_id 'msg-1', got %q", pending[0].MessageID)
	}
	if pending[0].AgentID != "ag-b" {
		t.Fatalf("expected agent_id 'ag-b', got %q", pending[0].AgentID)
	}
	if pending[0].CreatedAt != "2026-02-18T10:00:00Z" {
		t.Fatalf("expected created_at '2026-02-18T10:00:00Z', got %q", pending[0].CreatedAt)
	}
}

func TestClaudeCodePushAppendsMultiple(t *testing.T) {
	dir := t.TempDir()
	a := NewClaudeCodeAdapter(dir)

	msg1 := Message{ID: "msg-1", From: "ag-a", To: "ag-b", Body: "first"}
	msg2 := Message{ID: "msg-2", From: "ag-c", To: "ag-b", Body: "second"}
	msg3 := Message{ID: "msg-3", From: "ag-a", To: "ag-b", Body: "third"}

	for _, msg := range []Message{msg1, msg2, msg3} {
		if err := a.Push("sess-1", "ag-b", msg); err != nil {
			t.Fatalf("push failed: %v", err)
		}
	}

	stateFile := filepath.Join(dir, "pending-messages.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}

	var pending []pendingMessage
	if err := json.Unmarshal(data, &pending); err != nil {
		t.Fatalf("unmarshal state file: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("expected 3 pending messages, got %d", len(pending))
	}
	if pending[0].Body != "first" {
		t.Fatalf("expected first message body 'first', got %q", pending[0].Body)
	}
	if pending[1].From != "ag-c" {
		t.Fatalf("expected second message from 'ag-c', got %q", pending[1].From)
	}
	if pending[2].Body != "third" {
		t.Fatalf("expected third message body 'third', got %q", pending[2].Body)
	}
}

func TestClaudeCodePushCreatesDirectory(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "deep", "nested", "dir")
	a := NewClaudeCodeAdapter(nested)

	msg := Message{ID: "msg-1", From: "ag-a", To: "ag-b", Body: "hello"}
	if err := a.Push("sess-1", "ag-b", msg); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	stateFile := filepath.Join(nested, "pending-messages.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file should exist after push: %v", err)
	}
}

func TestClaudeCodeStateFileMatchesStopHookFormat(t *testing.T) {
	dir := t.TempDir()
	a := NewClaudeCodeAdapter(dir)

	msg := Message{ID: "msg-42", From: "agent-alpha", To: "agent-beta", Body: "relay payload here"}
	if err := a.Push("sess-1", "agent-beta", msg); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	stateFile := filepath.Join(dir, "pending-messages.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}

	// The stop hook uses jq to access .from and .body on each array element.
	// Verify raw JSON structure matches expectations.
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("state file is not a JSON array of objects: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(raw))
	}
	entry := raw[0]
	if entry["from"] != "agent-alpha" {
		t.Fatalf("expected from 'agent-alpha', got %v", entry["from"])
	}
	if entry["body"] != "relay payload here" {
		t.Fatalf("expected body 'relay payload here', got %v", entry["body"])
	}
	// Verify additional fields are present.
	if entry["message_id"] != "msg-42" {
		t.Fatalf("expected message_id 'msg-42', got %v", entry["message_id"])
	}
	if entry["agent_id"] != "agent-beta" {
		t.Fatalf("expected agent_id 'agent-beta', got %v", entry["agent_id"])
	}
}

func TestClaudeCodePushHandlesCorruptedStateFile(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "pending-messages.json")

	// Write garbage to the state file.
	if err := os.WriteFile(stateFile, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	a := NewClaudeCodeAdapter(dir)
	msg := Message{ID: "msg-1", From: "ag-a", To: "ag-b", Body: "recovery"}
	if err := a.Push("sess-1", "ag-b", msg); err != nil {
		t.Fatalf("push should recover from corrupted state: %v", err)
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var pending []pendingMessage
	if err := json.Unmarshal(data, &pending); err != nil {
		t.Fatalf("unmarshal state file: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending message after recovery, got %d", len(pending))
	}
	if pending[0].Body != "recovery" {
		t.Fatalf("expected body 'recovery', got %q", pending[0].Body)
	}
}
