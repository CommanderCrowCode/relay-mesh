package push

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"strings"
	"time"
)

// OpenCodeAdapter delivers push notifications via the OpenCode HTTP API.
type OpenCodeAdapter struct {
	baseURL  string
	client   *http.Client
	noReply  bool
	disabled bool
}

// NewOpenCodeAdapter creates an adapter for OpenCode push delivery.
// An empty baseURL disables the adapter.
func NewOpenCodeAdapter(baseURL string, timeout time.Duration, noReply bool) *OpenCodeAdapter {
	baseURL = strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if baseURL == "" {
		return &OpenCodeAdapter{disabled: true}
	}
	return &OpenCodeAdapter{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		noReply: noReply,
	}
}

func (a *OpenCodeAdapter) HarnessType() string { return "opencode" }

func (a *OpenCodeAdapter) Enabled() bool { return !a.disabled }

func (a *OpenCodeAdapter) Push(sessionID, agentID string, msg Message) error {
	if a.disabled {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}

	body := map[string]any{
		"noReply": a.noReply,
		"parts": []map[string]string{
			{
				"type": "text",
				"text": fmt.Sprintf(
					"New relay-mesh message for %s.\nfrom: %s\nmessage_id: %s\nbody:\n%s",
					agentID,
					msg.From,
					msg.ID,
					msg.Body,
				),
			},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal push request: %w", err)
	}

	url := fmt.Sprintf("%s/session/%s/prompt_async", a.baseURL, sessionID)
	if err := a.postJSONExpect(url, data, http.StatusNoContent); err != nil {
		return fmt.Errorf("post prompt_async: %w", err)
	}

	// Best-effort UI visibility signal in OpenCode TUI.
	toast := map[string]any{
		"title":   "relay-mesh",
		"message": fmt.Sprintf("New message for %s from %s", agentID, msg.From),
		"variant": "info",
	}
	toastData, _ := json.Marshal(toast)
	toastURL := fmt.Sprintf("%s/tui/show-toast", a.baseURL)
	if directory, err := a.sessionDirectory(sessionID); err == nil && strings.TrimSpace(directory) != "" {
		toastURL = toastURL + "?directory=" + urlpkg.QueryEscape(directory)
	}
	_ = a.postJSONExpect(toastURL, toastData, http.StatusOK)

	return nil
}

func (a *OpenCodeAdapter) sessionDirectory(sessionID string) (string, error) {
	sessionURL := fmt.Sprintf("%s/session/%s", a.baseURL, sessionID)
	req, err := http.NewRequest(http.MethodGet, sessionURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("session lookup status %d", resp.StatusCode)
	}
	var payload struct {
		Directory string `json:"directory"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2048)).Decode(&payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Directory), nil
}

func (a *OpenCodeAdapter) postJSONExpect(url string, body []byte, expected int) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != expected {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
