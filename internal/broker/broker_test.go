package broker

import (
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

func runNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()

	s, err := natsserver.NewServer(&natsserver.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	})
	if err != nil {
		t.Fatalf("new nats server: %v", err)
	}

	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		s.Shutdown()
		t.Fatal("nats server not ready")
	}

	t.Cleanup(func() {
		s.Shutdown()
		s.WaitForShutdown()
	})

	return s
}

func newTestBroker(t *testing.T) *Broker {
	t.Helper()

	s := runNATSServer(t)
	b, err := New(s.ClientURL())
	if err != nil {
		t.Fatalf("create broker: %v", err)
	}

	t.Cleanup(func() {
		b.Close()
	})

	return b
}

func waitForQueuedMessages(t *testing.T, b *Broker, agentID string, minCount int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		agent := b.agents[agentID]
		count := 0
		if agent != nil {
			count = len(agent.Queue)
		}
		b.mu.Unlock()

		if count >= minCount {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %d queued messages for %s", minCount, agentID)
}

func TestRegisterSendAndFetch(t *testing.T) {
	b := newTestBroker(t)

	fromID, err := b.RegisterAgent("alice")
	if err != nil {
		t.Fatalf("register sender: %v", err)
	}
	toID, err := b.RegisterAgent("bob")
	if err != nil {
		t.Fatalf("register receiver: %v", err)
	}
	unnamedID, err := b.RegisterAgent("   ")
	if err != nil {
		t.Fatalf("register unnamed agent: %v", err)
	}

	agents := b.ListAgents()
	if len(agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(agents))
	}
	seenFrom := false
	seenTo := false
	seenUnnamed := false
	for _, a := range agents {
		if a["id"] == fromID && a["name"] == "alice" {
			seenFrom = true
		}
		if a["id"] == toID && a["name"] == "bob" {
			seenTo = true
		}
		if a["id"] == unnamedID && a["name"] == unnamedID {
			seenUnnamed = true
		}
	}
	if !seenFrom || !seenTo || !seenUnnamed {
		t.Fatalf("list_agents missing expected entries: %#v", agents)
	}

	msg, err := b.Send(fromID, toID, "hello")
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if !strings.HasPrefix(msg.ID, "msg-") {
		t.Fatalf("expected msg id prefix, got %q", msg.ID)
	}
	if msg.From != fromID || msg.To != toID || msg.Body != "hello" {
		t.Fatalf("unexpected message envelope: %#v", msg)
	}
	waitForQueuedMessages(t, b, toID, 1)

	got, err := b.Fetch(toID, 10)
	if err != nil {
		t.Fatalf("fetch messages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].ID != msg.ID {
		t.Fatalf("fetched message id mismatch: got %q want %q", got[0].ID, msg.ID)
	}

	got, err = b.Fetch(toID, 10)
	if err != nil {
		t.Fatalf("fetch empty queue: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty queue after fetch, got %d messages", len(got))
	}
}

func TestSendRejectsUnknownSender(t *testing.T) {
	b := newTestBroker(t)

	toID, err := b.RegisterAgent("bob")
	if err != nil {
		t.Fatalf("register receiver: %v", err)
	}

	_, err = b.Send("ag-missing", toID, "hello")
	if err == nil {
		t.Fatal("expected send to fail for unknown sender")
	}
	if !strings.Contains(err.Error(), "sender agent not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendRejectsUnknownTarget(t *testing.T) {
	b := newTestBroker(t)

	fromID, err := b.RegisterAgent("alice")
	if err != nil {
		t.Fatalf("register sender: %v", err)
	}

	_, err = b.Send(fromID, "ag-missing", "hello")
	if err == nil {
		t.Fatal("expected send to fail for unknown target")
	}
	if !strings.Contains(err.Error(), "target agent not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFetchDefaultLimitAndDrain(t *testing.T) {
	b := newTestBroker(t)

	fromID, err := b.RegisterAgent("source")
	if err != nil {
		t.Fatalf("register sender: %v", err)
	}
	toID, err := b.RegisterAgent("sink")
	if err != nil {
		t.Fatalf("register receiver: %v", err)
	}

	for i := 0; i < 12; i++ {
		if _, err := b.Send(fromID, toID, "payload"); err != nil {
			t.Fatalf("send message %d: %v", i, err)
		}
	}
	waitForQueuedMessages(t, b, toID, 12)

	firstBatch, err := b.Fetch(toID, 0)
	if err != nil {
		t.Fatalf("fetch default batch: %v", err)
	}
	if len(firstBatch) != 10 {
		t.Fatalf("expected default fetch size 10, got %d", len(firstBatch))
	}

	secondBatch, err := b.Fetch(toID, 10)
	if err != nil {
		t.Fatalf("fetch second batch: %v", err)
	}
	if len(secondBatch) != 2 {
		t.Fatalf("expected 2 remaining messages, got %d", len(secondBatch))
	}
}
