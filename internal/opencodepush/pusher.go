package opencodepush

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("post prompt_async: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("prompt_async status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
