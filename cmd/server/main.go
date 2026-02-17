package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nats-io/nats.go"

	"github.com/tanwa/relay-mesh/internal/broker"
)

func main() {
	natsURL := getenv("NATS_URL", nats.DefaultURL)

	b, err := broker.New(natsURL)
	if err != nil {
		slog.Error("failed to initialize broker", "error", err)
		os.Exit(1)
	}
	defer b.Close()

	s := server.NewMCPServer(
		"relay-mesh",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	registerTool := mcp.NewTool(
		"register_agent",
		mcp.WithDescription("Register an anonymous agent and return an agent_id."),
		mcp.WithString("name", mcp.Description("Optional display name for this agent.")),
	)
	listTool := mcp.NewTool(
		"list_agents",
		mcp.WithDescription("List all connected anonymous agents."),
	)
	sendTool := mcp.NewTool(
		"send_message",
		mcp.WithDescription("Send a message from one agent to another using NATS."),
		mcp.WithString("from", mcp.Required(), mcp.Description("Sender agent_id.")),
		mcp.WithString("to", mcp.Required(), mcp.Description("Recipient agent_id.")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Message body.")),
	)
	fetchTool := mcp.NewTool(
		"fetch_messages",
		mcp.WithDescription("Fetch pending messages for an agent."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("Agent id to fetch for.")),
		mcp.WithString("max", mcp.Description("Max number of messages to fetch (default 10).")),
	)

	s.AddTool(registerTool, registerHandler(b))
	s.AddTool(listTool, listHandler(b))
	s.AddTool(sendTool, sendHandler(b))
	s.AddTool(fetchTool, fetchHandler(b))

	if err := server.ServeStdio(s); err != nil {
		slog.Error("mcp server stopped", "error", err)
		os.Exit(1)
	}
}

func registerHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.GetString("name", "")
		id, err := b.RegisterAgent(name)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		out := map[string]string{"agent_id": id}
		body, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func listHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		body, _ := json.Marshal(b.ListAgents())
		return mcp.NewToolResultText(string(body)), nil
	}
}

func sendHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		from := req.GetString("from", "")
		to := req.GetString("to", "")
		msgBody := req.GetString("body", "")
		if from == "" || to == "" || msgBody == "" {
			return mcp.NewToolResultError("from, to, and body are required"), nil
		}

		msg, err := b.Send(from, to, msgBody)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, _ := json.Marshal(msg)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func fetchHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		agentID := req.GetString("agent_id", "")
		if agentID == "" {
			return mcp.NewToolResultError("agent_id is required"), nil
		}

		maxText := req.GetString("max", "10")
		max, err := strconv.Atoi(maxText)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid max: %s", maxText)), nil
		}

		messages, err := b.Fetch(agentID, max)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, _ := json.Marshal(messages)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
