package opencodepush

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type SessionResolver struct {
	baseURL string
	client  *http.Client
	enabled bool
	window  time.Duration
}

type openCodeSession struct {
	ID   string `json:"id"`
	Time struct {
		Updated int64 `json:"updated"`
	} `json:"time"`
}

func NewSessionResolver(baseURL string, timeout time.Duration, window time.Duration) *SessionResolver {
	baseURL = strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	if baseURL == "" {
		return &SessionResolver{enabled: false}
	}
	return &SessionResolver{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		enabled: true,
		window:  window,
	}
}

func (r *SessionResolver) Enabled() bool {
	return r.enabled
}

func (r *SessionResolver) FindLatestUnboundSession(bound map[string]struct{}) (string, error) {
	if !r.enabled {
		return "", nil
	}

	req, err := http.NewRequest(http.MethodGet, r.baseURL+"/session", nil)
	if err != nil {
		return "", fmt.Errorf("build session list request: %w", err)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request session list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("session list status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var sessions []openCodeSession
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return "", fmt.Errorf("decode session list: %w", err)
	}
	if len(sessions) == 0 {
		return "", nil
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Time.Updated > sessions[j].Time.Updated
	})

	now := time.Now()
	for _, s := range sessions {
		if strings.TrimSpace(s.ID) == "" {
			continue
		}
		if _, used := bound[s.ID]; used {
			continue
		}
		updatedAt := unixMaybeMillis(s.Time.Updated)
		if updatedAt.IsZero() {
			continue
		}
		if now.Sub(updatedAt) > r.window {
			continue
		}
		return s.ID, nil
	}
	return "", nil
}

func unixMaybeMillis(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	// OpenCode session timestamps are milliseconds since epoch.
	if v > 1_000_000_000_000 {
		return time.UnixMilli(v)
	}
	return time.Unix(v, 0)
}
