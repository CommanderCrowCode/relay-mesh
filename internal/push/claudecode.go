package push

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// ClaudeCodeAdapter implements push delivery for Claude Code.
// Since Claude Code has no prompt injection API, this adapter:
// 1. Writes pending messages to a state file for the Stop hook to read
// 2. Sends a desktop notification via notify-send (Linux) or osascript (macOS)
type ClaudeCodeAdapter struct {
	stateDir string // e.g., ~/.relay-mesh/claude-code/
	mu       sync.Mutex
}

// pendingMessage is the JSON structure written to pending-messages.json.
// Fields must match what relay-stop.sh expects (from, body at minimum).
type pendingMessage struct {
	From      string `json:"from"`
	Body      string `json:"body"`
	MessageID string `json:"message_id"`
	AgentID   string `json:"agent_id"`
	CreatedAt string `json:"created_at"`
}

// NewClaudeCodeAdapter creates an adapter that writes pending messages to stateDir.
func NewClaudeCodeAdapter(stateDir string) *ClaudeCodeAdapter {
	return &ClaudeCodeAdapter{stateDir: stateDir}
}

func (a *ClaudeCodeAdapter) HarnessType() string { return "claude-code" }

func (a *ClaudeCodeAdapter) Enabled() bool { return true }

func (a *ClaudeCodeAdapter) Push(sessionID, agentID string, msg Message) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := os.MkdirAll(a.stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	stateFile := filepath.Join(a.stateDir, "pending-messages.json")

	// Read existing pending messages.
	var pending []pendingMessage
	data, err := os.ReadFile(stateFile)
	if err == nil {
		if err := json.Unmarshal(data, &pending); err != nil {
			// Corrupted file; start fresh.
			pending = nil
		}
	}

	// Append new message.
	pending = append(pending, pendingMessage{
		From:      msg.From,
		Body:      msg.Body,
		MessageID: msg.ID,
		AgentID:   agentID,
		CreatedAt: msg.CreatedAt,
	})

	// Marshal updated array.
	out, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pending messages: %w", err)
	}

	// Atomic write: temp file in same dir + rename.
	tmp, err := os.CreateTemp(a.stateDir, "pending-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, stateFile); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp to state file: %w", err)
	}

	// Best-effort desktop notification.
	a.sendNotification(agentID, msg.From)

	return nil
}

// sendNotification sends a best-effort desktop notification. Errors are ignored.
func (a *ClaudeCodeAdapter) sendNotification(agentID, from string) {
	text := fmt.Sprintf("New message for %s from %s", agentID, from)

	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("notify-send", "relay-mesh", text).Run()
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title "relay-mesh"`, text)
		_ = exec.Command("osascript", "-e", script).Run()
	}
}
