package broker

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const subjectPrefix = "relay.agent"

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
	Queue     []Message
}

// Broker stores anonymous agent routing state and uses NATS as transport.
type Broker struct {
	mu     sync.Mutex
	nc     *nats.Conn
	agents map[string]*agentState
	subs   map[string]*nats.Subscription
}

func New(natsURL string) (*Broker, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("connect to nats: %w", err)
	}
	return &Broker{
		nc:     nc,
		agents: make(map[string]*agentState),
		subs:   make(map[string]*nats.Subscription),
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

	state := &agentState{ID: id, Profile: profile, Subject: subject}
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
	}, nil
}

func (b *Broker) FindAgents(filter AgentSearchFilter) []map[string]string {
	filter = normalizeFilter(filter)
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]map[string]string, 0)
	for _, a := range b.agents {
		if !matchAgent(a.Profile, filter) {
			continue
		}
		out = append(out, map[string]string{
			"id":             a.ID,
			"name":           a.Profile.Name,
			"description":    a.Profile.Description,
			"project":        a.Profile.Project,
			"role":           a.Profile.Role,
			"github":         a.Profile.GitHub,
			"branch":         a.Profile.Branch,
			"specialization": a.Profile.Specialization,
		})
		if len(out) >= filter.Limit {
			break
		}
	}
	return out
}

func (b *Broker) BindSession(agentID, sessionID string) error {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
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

	if err := b.nc.Publish(toAgent.Subject, data); err != nil {
		return Message{}, fmt.Errorf("publish: %w", err)
	}
	if err := b.nc.Flush(); err != nil {
		return Message{}, fmt.Errorf("flush: %w", err)
	}

	return m, nil
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
	targets := make([]string, 0)
	for id, a := range b.agents {
		if id == from {
			continue
		}
		if !matchAgent(a.Profile, filter) {
			continue
		}
		targets = append(targets, id)
		if len(targets) >= filter.Limit {
			break
		}
	}
	b.mu.Unlock()

	out := make([]Message, 0, len(targets))
	for _, to := range targets {
		msg, err := b.Send(from, to, body)
		if err != nil {
			return out, err
		}
		out = append(out, msg)
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

func randomID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random id: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(buf), nil
}

func normalizeProfile(p AgentProfile) AgentProfile {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Project = strings.TrimSpace(p.Project)
	p.Role = strings.TrimSpace(p.Role)
	p.GitHub = strings.TrimSpace(p.GitHub)
	p.Branch = strings.TrimSpace(p.Branch)
	p.Specialization = strings.TrimSpace(p.Specialization)
	return p
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

func matchAgent(p AgentProfile, f AgentSearchFilter) bool {
	project := strings.ToLower(p.Project)
	role := strings.ToLower(p.Role)
	spec := strings.ToLower(p.Specialization)
	name := strings.ToLower(p.Name)
	desc := strings.ToLower(p.Description)
	gh := strings.ToLower(p.GitHub)
	branch := strings.ToLower(p.Branch)

	if f.Project != "" && project != f.Project {
		return false
	}
	if f.Role != "" && role != f.Role {
		return false
	}
	if f.Specialization != "" && spec != f.Specialization {
		return false
	}
	if f.Query != "" {
		hay := []string{name, desc, project, role, spec, gh, branch}
		found := false
		for _, v := range hay {
			if strings.Contains(v, f.Query) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
