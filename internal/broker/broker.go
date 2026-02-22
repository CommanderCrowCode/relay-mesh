package broker

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/nats-io/nats.go"
)

const subjectPrefix = "relay.agent"
const streamName = "RELAY_MESSAGES"

// Message is the minimal NATS message envelope for this POC.
type Message struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type AgentProfile struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Project        string `json:"project"`
	Role           string `json:"role"`
	GitHub         string `json:"github,omitempty"`
	Branch         string `json:"branch,omitempty"`
	Specialization string `json:"specialization"`
	Status         string `json:"status,omitempty"` // "idle" | "working" | "blocked" | "done"
}

// AgentStatusEntry is a snapshot of an agent's current state for team coordination.
type AgentStatusEntry struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Role           string    `json:"role"`
	Project        string    `json:"project"`
	Status         string    `json:"status"`
	LastSeen       time.Time `json:"last_seen"`
	LastFetch      time.Time `json:"last_fetch"`
	UnreadMessages int       `json:"unread_messages"`
}

type AgentSearchFilter struct {
	Query          string
	Project        string
	Role           string
	Specialization string
	Limit          int
}

type agentState struct {
	ID        string
	Profile   AgentProfile
	Subject   string
	SessionID string
	Harness   string // "opencode", "claude-code", "codex", "generic"
	Queue     []Message
	LastSeen  time.Time
	LastFetch time.Time
}

// Broker stores anonymous agent routing state and uses NATS as transport.
type Broker struct {
	mu           sync.Mutex
	nc           *nats.Conn
	js           nats.JetStreamContext
	agents       map[string]*agentState
	subs         map[string]*nats.Subscription
	sessionIndex map[string]string            // session_id → agent_id
	contextStore map[string]map[string]string // project → key → value
}

func New(natsURL string) (*Broker, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("connect to nats: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		_ = nc.Drain()
		return nil, fmt.Errorf("init jetstream context: %w", err)
	}
	if err := ensureStream(js); err != nil {
		_ = nc.Drain()
		return nil, err
	}
	return &Broker{
		nc:           nc,
		js:           js,
		agents:       make(map[string]*agentState),
		subs:         make(map[string]*nats.Subscription),
		sessionIndex: make(map[string]string),
		contextStore: make(map[string]map[string]string),
	}, nil
}

func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, sub := range b.subs {
		_ = sub.Unsubscribe()
	}
	b.subs = make(map[string]*nats.Subscription)

	if b.nc != nil {
		b.nc.Close()
	}
}

func (b *Broker) RegisterAgent(profile AgentProfile) (string, error) {
	profile = normalizeProfile(profile)
	if err := validateProfile(profile); err != nil {
		return "", err
	}

	id, err := randomID("ag")
	if err != nil {
		return "", err
	}

	subject := fmt.Sprintf("%s.%s", subjectPrefix, id)
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = id
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if profile.Status == "" {
		profile.Status = "idle"
	}
	state := &agentState{ID: id, Profile: profile, Subject: subject, LastSeen: time.Now().UTC()}
	sub, err := b.nc.Subscribe(subject, func(msg *nats.Msg) {
		var incoming Message
		if err := json.Unmarshal(msg.Data, &incoming); err != nil {
			return
		}

		b.mu.Lock()
		defer b.mu.Unlock()
		a := b.agents[id]
		if a == nil {
			return
		}
		a.Queue = append(a.Queue, incoming)
	})
	if err != nil {
		return "", fmt.Errorf("subscribe: %w", err)
	}
	if err := b.nc.Flush(); err != nil {
		_ = sub.Unsubscribe()
		return "", fmt.Errorf("flush subscription: %w", err)
	}

	b.agents[id] = state
	b.subs[id] = sub
	return id, nil
}

func (b *Broker) RegisterOrUpdateBySession(sessionID string, profile AgentProfile) (agentID string, created bool, err error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		id, err := b.RegisterAgent(profile)
		return id, true, err
	}

	b.mu.Lock()
	existingID, found := b.sessionIndex[sessionID]
	if found {
		agent := b.agents[existingID]
		if agent == nil {
			// Stale index entry; remove and treat as new.
			delete(b.sessionIndex, sessionID)
			b.mu.Unlock()

			id, err := b.RegisterAgent(profile)
			if err != nil {
				return "", false, err
			}
			b.mu.Lock()
			b.sessionIndex[sessionID] = id
			if a := b.agents[id]; a != nil {
				a.SessionID = sessionID
			}
			b.mu.Unlock()
			return id, true, nil
		}

		// Dedup: update existing agent's profile.
		applyProfilePatch(&agent.Profile, profile)
		agent.Profile = normalizeProfile(agent.Profile)
		if err := validateProfile(agent.Profile); err != nil {
			b.mu.Unlock()
			return "", false, err
		}
		// Re-bind session to preserve harness binding.
		agent.SessionID = sessionID
		agent.LastSeen = time.Now().UTC()
		b.mu.Unlock()
		return existingID, false, nil
	}
	b.mu.Unlock()

	// New session — register normally then index.
	id, err := b.RegisterAgent(profile)
	if err != nil {
		return "", false, err
	}
	b.mu.Lock()
	b.sessionIndex[sessionID] = id
	if a := b.agents[id]; a != nil {
		a.SessionID = sessionID
	}
	b.mu.Unlock()
	return id, true, nil
}

func (b *Broker) ListAgents() []map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]map[string]string, 0, len(b.agents))
	for _, a := range b.agents {
		out = append(out, map[string]string{
			"id":             a.ID,
			"name":           a.Profile.Name,
			"description":    a.Profile.Description,
			"project":        a.Profile.Project,
			"role":           a.Profile.Role,
			"github":         a.Profile.GitHub,
			"branch":         a.Profile.Branch,
			"specialization": a.Profile.Specialization,
			"status":         a.Profile.Status,
			"last_seen":      a.LastSeen.Format(time.RFC3339),
		})
	}
	return out
}

func (b *Broker) UpdateAgentProfile(agentID string, patch AgentProfile) (map[string]string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	agent := b.agents[agentID]
	if agent == nil {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	applyProfilePatch(&agent.Profile, patch)
	agent.Profile = normalizeProfile(agent.Profile)
	if err := validateProfile(agent.Profile); err != nil {
		return nil, err
	}

	return map[string]string{
		"id":             agent.ID,
		"name":           agent.Profile.Name,
		"description":    agent.Profile.Description,
		"project":        agent.Profile.Project,
		"role":           agent.Profile.Role,
		"github":         agent.Profile.GitHub,
		"branch":         agent.Profile.Branch,
		"specialization": agent.Profile.Specialization,
		"status":         agent.Profile.Status,
		"last_seen":      agent.LastSeen.Format(time.RFC3339),
	}, nil
}

func (b *Broker) FindAgents(filter AgentSearchFilter) []map[string]string {
	filter = normalizeFilter(filter)
	b.mu.Lock()
	defer b.mu.Unlock()

	type candidate struct {
		agent         *agentState
		score         int
		matchedTokens int
	}
	all := make([]candidate, 0, len(b.agents))
	totalTokens := len(tokenize(filter.Query))

	for _, a := range b.agents {
		score, matchedTokens, ok := matchAgent(a.Profile, filter)
		if !ok {
			continue
		}
		all = append(all, candidate{agent: a, score: score, matchedTokens: matchedTokens})
	}
	if len(all) == 0 {
		return []map[string]string{}
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].score == all[j].score {
			return all[i].agent.ID < all[j].agent.ID
		}
		return all[i].score > all[j].score
	})

	primary := make([]candidate, 0, len(all))
	fallback := make([]candidate, 0, len(all))
	for _, c := range all {
		// Query fallback: if no full token match exists, return best fuzzy suggestions.
		if totalTokens == 0 || c.matchedTokens >= totalTokens {
			primary = append(primary, c)
			continue
		}
		if c.matchedTokens > 0 {
			fallback = append(fallback, c)
		}
	}

	chosen := primary
	if len(chosen) == 0 && totalTokens > 0 {
		chosen = fallback
	}

	out := make([]map[string]string, 0, min(filter.Limit, len(chosen)))
	for _, c := range chosen {
		a := c.agent
		out = append(out, map[string]string{
			"id":             a.ID,
			"name":           a.Profile.Name,
			"description":    a.Profile.Description,
			"project":        a.Profile.Project,
			"role":           a.Profile.Role,
			"github":         a.Profile.GitHub,
			"branch":         a.Profile.Branch,
			"specialization": a.Profile.Specialization,
			"status":         a.Profile.Status,
			"last_seen":      a.LastSeen.Format(time.RFC3339),
		})
		if len(out) >= filter.Limit {
			break
		}
	}
	return out
}

func (b *Broker) BindSession(agentID, sessionID, harness string) error {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	harness = strings.TrimSpace(harness)
	if agentID == "" || sessionID == "" {
		return fmt.Errorf("agent_id and session_id are required")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	agent := b.agents[agentID]
	if agent == nil {
		return fmt.Errorf("agent not found: %s", agentID)
	}
	agent.SessionID = sessionID
	if harness != "" {
		agent.Harness = harness
	}
	return nil
}

func (b *Broker) GetSessionBinding(agentID string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	agent := b.agents[agentID]
	if agent == nil || strings.TrimSpace(agent.SessionID) == "" {
		return "", false
	}
	return agent.SessionID, true
}

func (b *Broker) GetSessionBindingWithHarness(agentID string) (sessionID string, harness string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	agent := b.agents[agentID]
	if agent == nil || strings.TrimSpace(agent.SessionID) == "" {
		return "", "", false
	}
	return agent.SessionID, agent.Harness, true
}

func (b *Broker) ListBoundSessionIDs() map[string]struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make(map[string]struct{})
	for _, a := range b.agents {
		if s := strings.TrimSpace(a.SessionID); s != "" {
			out[s] = struct{}{}
		}
	}
	return out
}

func (b *Broker) Send(from, to, body string) (Message, error) {
	b.mu.Lock()
	fromAgent := b.agents[from]
	toAgent := b.agents[to]
	if fromAgent != nil {
		fromAgent.LastSeen = time.Now().UTC()
	}
	b.mu.Unlock()

	if fromAgent == nil {
		return Message{}, fmt.Errorf("sender agent not found: %s", from)
	}
	if toAgent == nil {
		return Message{}, fmt.Errorf("target agent not found: %s", to)
	}

	id, err := randomID("msg")
	if err != nil {
		return Message{}, err
	}
	m := Message{
		ID:        id,
		From:      from,
		To:        to,
		Body:      body,
		CreatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(m)
	if err != nil {
		return Message{}, fmt.Errorf("marshal message: %w", err)
	}

	if _, err := b.js.Publish(toAgent.Subject, data); err != nil {
		return Message{}, fmt.Errorf("jetstream publish: %w", err)
	}

	return m, nil
}

func (b *Broker) FetchHistory(agentID string, max int) ([]Message, error) {
	if max <= 0 {
		max = 20
	}

	b.mu.Lock()
	agent := b.agents[agentID]
	b.mu.Unlock()
	if agent == nil {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	info, err := b.js.StreamInfo(streamName)
	if err != nil {
		return nil, fmt.Errorf("stream info: %w", err)
	}
	if info == nil || info.State.Msgs == 0 {
		return []Message{}, nil
	}

	out := make([]Message, 0, max)
	firstSeq := info.State.FirstSeq
	lastSeq := info.State.LastSeq

	for seq := lastSeq; seq >= firstSeq && len(out) < max; seq-- {
		stored, err := b.js.GetMsg(streamName, seq)
		if err != nil {
			continue
		}
		var msg Message
		if err := json.Unmarshal(stored.Data, &msg); err != nil {
			continue
		}
		if msg.To != agentID {
			continue
		}
		out = append(out, msg)
		if seq == firstSeq {
			break
		}
	}

	// Return oldest-to-newest for stable consumption.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (b *Broker) Broadcast(from, body string, filter AgentSearchFilter) ([]Message, error) {
	filter = normalizeFilter(filter)
	if strings.TrimSpace(from) == "" {
		return nil, fmt.Errorf("sender agent_id is required")
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("body is required")
	}

	b.mu.Lock()
	if b.agents[from] == nil {
		b.mu.Unlock()
		return nil, fmt.Errorf("sender agent not found: %s", from)
	}
	b.agents[from].LastSeen = time.Now().UTC()
	type targetCandidate struct {
		id    string
		score int
	}
	targets := make([]targetCandidate, 0)
	totalTokens := len(tokenize(filter.Query))
	for id, a := range b.agents {
		if id == from {
			continue
		}
		score, matchedTokens, ok := matchAgent(a.Profile, filter)
		if !ok {
			continue
		}
		// Apply same "full match first, fuzzy fallback" strategy as find_agents.
		if totalTokens > 0 && matchedTokens < totalTokens {
			score -= 100
		}
		targets = append(targets, targetCandidate{id: id, score: score})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].score == targets[j].score {
			return targets[i].id < targets[j].id
		}
		return targets[i].score > targets[j].score
	})
	b.mu.Unlock()

	out := make([]Message, 0, min(filter.Limit, len(targets)))
	for _, to := range targets {
		msg, err := b.Send(from, to.id, body)
		if err != nil {
			return out, err
		}
		out = append(out, msg)
		if len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (b *Broker) Fetch(agentID string, max int) ([]Message, error) {
	if max <= 0 {
		max = 10
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	agent := b.agents[agentID]
	if agent == nil {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}
	now := time.Now().UTC()
	agent.LastSeen = now
	agent.LastFetch = now
	if len(agent.Queue) == 0 {
		return []Message{}, nil
	}
	if max > len(agent.Queue) {
		max = len(agent.Queue)
	}

	out := make([]Message, max)
	copy(out, agent.Queue[:max])
	agent.Queue = agent.Queue[max:]
	return out, nil
}

// UnreadCount returns the number of pending messages in an agent's queue.
func (b *Broker) UnreadCount(agentID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.agents[agentID]
	if a == nil {
		return 0
	}
	return len(a.Queue)
}

// GetTeamStatus returns a snapshot of all agents matching the project filter.
// If project is empty, all agents are returned.
func (b *Broker) GetTeamStatus(project string) []AgentStatusEntry {
	project = strings.ToLower(strings.TrimSpace(project))
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]AgentStatusEntry, 0, len(b.agents))
	for _, a := range b.agents {
		if project != "" && !strings.Contains(strings.ToLower(a.Profile.Project), project) {
			continue
		}
		out = append(out, AgentStatusEntry{
			ID:             a.ID,
			Name:           a.Profile.Name,
			Role:           a.Profile.Role,
			Project:        a.Profile.Project,
			Status:         a.Profile.Status,
			LastSeen:       a.LastSeen,
			LastFetch:      a.LastFetch,
			UnreadMessages: len(a.Queue),
		})
	}
	return out
}

// SharedContextSet stores a key-value pair scoped to a project.
// Passing an empty value deletes the key.
func (b *Broker) SharedContextSet(project, key, value string) error {
	project = normalizeProjectName(project)
	key = strings.TrimSpace(key)
	if project == "" {
		return fmt.Errorf("project is required")
	}
	if key == "" {
		return fmt.Errorf("key is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.contextStore[project] == nil {
		b.contextStore[project] = make(map[string]string)
	}
	if value == "" {
		delete(b.contextStore[project], key)
	} else {
		b.contextStore[project][key] = value
	}
	return nil
}

// SharedContextGet retrieves a value from the shared project context.
func (b *Broker) SharedContextGet(project, key string) (string, bool) {
	project = normalizeProjectName(project)
	key = strings.TrimSpace(key)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.contextStore[project] == nil {
		return "", false
	}
	v, ok := b.contextStore[project][key]
	return v, ok
}

// SharedContextList returns a copy of all key-value pairs for a project.
func (b *Broker) SharedContextList(project string) map[string]string {
	project = normalizeProjectName(project)
	b.mu.Lock()
	defer b.mu.Unlock()
	src := b.contextStore[project]
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// WaitForAgents blocks until at least minCount agents are registered for the
// project, or until timeoutSec seconds have elapsed. Returns the agents found
// and whether the threshold was met.
func (b *Broker) WaitForAgents(project string, minCount int, timeoutSec int) ([]AgentStatusEntry, bool) {
	if minCount <= 0 {
		minCount = 2
	}
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for {
		agents := b.GetTeamStatus(project)
		if len(agents) >= minCount {
			return agents, true
		}
		if time.Now().After(deadline) {
			return agents, false
		}
		time.Sleep(2 * time.Second)
	}
}

func randomID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random id: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(buf), nil
}

func ensureStream(js nats.JetStreamContext) error {
	cfg := &nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{subjectPrefix + ".>"},
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
		Discard:   nats.DiscardOld,
		MaxAge:    7 * 24 * time.Hour,
	}

	if _, err := js.StreamInfo(streamName); err == nil {
		if _, err := js.UpdateStream(cfg); err != nil {
			return fmt.Errorf("update jetstream stream: %w", err)
		}
		return nil
	}
	if _, err := js.AddStream(cfg); err != nil {
		return fmt.Errorf("add jetstream stream: %w", err)
	}
	return nil
}

func normalizeProfile(p AgentProfile) AgentProfile {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Project = normalizeProjectName(p.Project)
	p.Role = strings.TrimSpace(p.Role)
	p.GitHub = strings.TrimSpace(p.GitHub)
	p.Branch = strings.TrimSpace(p.Branch)
	p.Specialization = strings.TrimSpace(p.Specialization)
	p.Status = strings.TrimSpace(p.Status)
	return p
}

func normalizeProjectName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// Insert hyphens at camelCase/PascalCase boundaries.
	runes := []rune(s)
	var buf strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			if unicode.IsLower(prev) {
				buf.WriteRune('-')
			} else if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				buf.WriteRune('-')
			}
		}
		buf.WriteRune(r)
	}
	s = buf.String()

	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	return s
}

func validateProfile(p AgentProfile) error {
	if p.Description == "" {
		return fmt.Errorf("description is required")
	}
	if p.Project == "" {
		return fmt.Errorf("project is required")
	}
	if p.Role == "" {
		return fmt.Errorf("role is required")
	}
	if p.Specialization == "" {
		return fmt.Errorf("specialization is required")
	}
	return nil
}

func applyProfilePatch(dst *AgentProfile, patch AgentProfile) {
	patch = normalizeProfile(patch)
	if patch.Name != "" {
		dst.Name = patch.Name
	}
	if patch.Description != "" {
		dst.Description = patch.Description
	}
	if patch.Project != "" {
		dst.Project = patch.Project
	}
	if patch.Role != "" {
		dst.Role = patch.Role
	}
	if patch.GitHub != "" {
		dst.GitHub = patch.GitHub
	}
	if patch.Branch != "" {
		dst.Branch = patch.Branch
	}
	if patch.Specialization != "" {
		dst.Specialization = patch.Specialization
	}
	if patch.Status != "" {
		dst.Status = patch.Status
	}
}

func normalizeFilter(f AgentSearchFilter) AgentSearchFilter {
	f.Query = strings.ToLower(strings.TrimSpace(f.Query))
	f.Project = strings.ToLower(strings.TrimSpace(f.Project))
	f.Role = strings.ToLower(strings.TrimSpace(f.Role))
	f.Specialization = strings.ToLower(strings.TrimSpace(f.Specialization))
	if f.Limit <= 0 {
		f.Limit = 20
	}
	return f
}

func matchAgent(p AgentProfile, f AgentSearchFilter) (int, int, bool) {
	project := strings.ToLower(p.Project)
	role := strings.ToLower(p.Role)
	spec := strings.ToLower(p.Specialization)
	name := strings.ToLower(p.Name)
	desc := strings.ToLower(p.Description)
	gh := strings.ToLower(p.GitHub)
	branch := strings.ToLower(p.Branch)

	score := 0
	if f.Project != "" {
		s, ok := fuzzyFieldMatch(f.Project, project)
		if !ok {
			return 0, 0, false
		}
		score += 300 + s
	}
	if f.Role != "" {
		s, ok := fuzzyFieldMatch(f.Role, role)
		if !ok {
			return 0, 0, false
		}
		score += 250 + s
	}
	if f.Specialization != "" {
		s, ok := fuzzyFieldMatch(f.Specialization, spec)
		if !ok {
			return 0, 0, false
		}
		score += 250 + s
	}

	matchedTokens := 0
	if f.Query != "" {
		queryTokens := tokenize(f.Query)
		hay := []string{name, desc, project, role, spec, gh, branch}
		for _, token := range queryTokens {
			best := 0
			ok := false
			for _, v := range hay {
				s, m := fuzzyFieldMatch(token, v)
				if !m {
					continue
				}
				ok = true
				if s > best {
					best = s
				}
			}
			if ok {
				matchedTokens++
				score += best
			}
		}
		// Need at least one meaningful hit for query mode.
		if matchedTokens == 0 {
			return 0, 0, false
		}
		// Prefer fuller matches but still allow fallback suggestions upstream.
		if matchedTokens < len(queryTokens) {
			score -= (len(queryTokens) - matchedTokens) * 30
		}
	} else {
		// No free-text query means this candidate should still rank stably.
		hay := []string{name, desc, project, role, spec, gh, branch}
		for _, v := range hay {
			if strings.TrimSpace(v) != "" {
				score += 1
				break
			}
		}
	}
	return score, matchedTokens, true
}

func fuzzyFieldMatch(needle, hay string) (int, bool) {
	needle = strings.ToLower(strings.TrimSpace(needle))
	hay = strings.ToLower(strings.TrimSpace(hay))
	if needle == "" || hay == "" {
		return 0, false
	}
	if hay == needle {
		return 200, true
	}
	if strings.HasPrefix(hay, needle) {
		return 180, true
	}
	if strings.Contains(hay, needle) {
		return 160, true
	}

	words := tokenize(hay)
	best := 0
	for _, w := range words {
		if w == needle {
			if 200 > best {
				best = 200
			}
			continue
		}
		if strings.HasPrefix(w, needle) || strings.HasPrefix(needle, w) {
			if 150 > best {
				best = 150
			}
			continue
		}
		dist := levenshtein(needle, w)
		maxDist := allowedDistance(max(len(needle), len(w)))
		if dist <= maxDist {
			s := 140 - (dist * 20)
			if s > best {
				best = s
			}
		}
	}
	if best > 0 {
		return best, true
	}
	return 0, false
}

func tokenize(s string) []string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func allowedDistance(n int) int {
	switch {
	case n <= 4:
		return 1
	case n <= 8:
		return 2
	default:
		return 3
	}
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min(del, min(ins, sub))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
