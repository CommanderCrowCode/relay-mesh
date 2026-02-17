package opencodepush

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"strings"
	"time"

	"github.com/tanwa/relay-mesh/internal/broker"
)

type Pusher struct {
	baseURL  string
	client   *http.Client
	noReply  bool
	disabled bool
}

func New(baseURL string, timeout time.Duration, noReply bool) *Pusher {
	baseURL = strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if baseURL == "" {
		return &Pusher{disabled: true}
	}
	return &Pusher{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		noReply: noReply,
	}
}

func (p *Pusher) Enabled() bool {
	return !p.disabled
}

func (p *Pusher) Push(sessionID, targetAgentID string, msg broker.Message) error {
	if p.disabled {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}

	body := map[string]any{
		"noReply": p.noReply,
		"parts": []map[string]string{
			{
				"type": "text",
				"text": fmt.Sprintf(
					"New relay-mesh message for %s.\nfrom: %s\nmessage_id: %s\nbody:\n%s",
					targetAgentID,
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

	url := fmt.Sprintf("%s/session/%s/prompt_async", p.baseURL, sessionID)
	if err := p.postJSONExpect(url, data, http.StatusNoContent); err != nil {
		return fmt.Errorf("post prompt_async: %w", err)
	}

	// Best-effort UI visibility signal in OpenCode TUI.
	toast := map[string]any{
		"title":   "relay-mesh",
		"message": fmt.Sprintf("New message for %s from %s", targetAgentID, msg.From),
		"variant": "info",
	}
	toastData, _ := json.Marshal(toast)
	toastURL := fmt.Sprintf("%s/tui/show-toast", p.baseURL)
	if directory, err := p.sessionDirectory(sessionID); err == nil && strings.TrimSpace(directory) != "" {
		toastURL = toastURL + "?directory=" + urlpkg.QueryEscape(directory)
	}
	_ = p.postJSONExpect(toastURL, toastData, http.StatusOK)

	return nil
}

func (p *Pusher) sessionDirectory(sessionID string) (string, error) {
	sessionURL := fmt.Sprintf("%s/session/%s", p.baseURL, sessionID)
	req, err := http.NewRequest(http.MethodGet, sessionURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := p.client.Do(req)
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

func (p *Pusher) postJSONExpect(url string, body []byte, expected int) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
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
