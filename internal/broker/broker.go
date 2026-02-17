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

type agentState struct {
	ID      string
	Name    string
	Subject string
	Queue   []Message
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

func (b *Broker) RegisterAgent(name string) (string, error) {
	id, err := randomID("ag")
	if err != nil {
		return "", err
	}

	subject := fmt.Sprintf("%s.%s", subjectPrefix, id)
	if strings.TrimSpace(name) == "" {
		name = id
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	state := &agentState{ID: id, Name: name, Subject: subject}
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
			"id":   a.ID,
			"name": a.Name,
		})
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
