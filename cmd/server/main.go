package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nats-io/nats.go"

	"github.com/tanwa/relay-mesh/internal/broker"
	"github.com/tanwa/relay-mesh/internal/opencodepush"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "serve", "run":
		runServer()
	case "version", "--version", "-v":
		fmt.Printf("relay-mesh %s (commit=%s built=%s)\n", Version, Commit, BuildDate)
	case "install-opencode-plugin":
		if err := installOpenCodePlugin(); err != nil {
			slog.Error("install-opencode-plugin failed", "error", err)
			os.Exit(1)
		}
	case "mesh-up", "up":
		if err := meshUp(); err != nil {
			slog.Error("mesh-up failed", "error", err)
			os.Exit(1)
		}
	case "mesh-down", "down":
		if err := meshDown(); err != nil {
			slog.Error("mesh-down failed", "error", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		fmt.Fprintf(os.Stderr, "usage: relay-mesh [serve|up|down|mesh-up|mesh-down|install-opencode-plugin|version]\n")
		os.Exit(2)
	}
}

func runServer() {
	natsURL := getenv("NATS_URL", nats.DefaultURL)

	b, err := broker.New(natsURL)
	if err != nil {
		slog.Error("failed to initialize broker", "error", err)
		os.Exit(1)
	}
	defer b.Close()
	pusher := opencodepush.New(
		getenv("OPENCODE_URL", ""),
		getDurationFromEnv("OPENCODE_PUSH_TIMEOUT", 15*time.Second),
		getBoolFromEnv("OPENCODE_NO_REPLY", false),
	)
	resolver := opencodepush.NewSessionResolver(
		getenv("OPENCODE_URL", ""),
		getDurationFromEnv("OPENCODE_PUSH_TIMEOUT", 15*time.Second),
		getDurationFromEnv("OPENCODE_AUTO_BIND_WINDOW", 15*time.Minute),
	)

	s := buildMCPServer(b, pusher, resolver)

	transport := getenv("MCP_TRANSPORT", "stdio")
	switch transport {
	case "stdio":
		if err := server.ServeStdio(s); err != nil {
			slog.Error("mcp server stopped", "error", err)
			os.Exit(1)
		}
	case "http":
		addr := getenv("MCP_HTTP_ADDR", "127.0.0.1:8080")
		path := getenv("MCP_HTTP_PATH", "/mcp")
		httpServer := server.NewStreamableHTTPServer(
			s,
			server.WithEndpointPath(path),
		)
		slog.Info("starting streamable HTTP MCP server", "addr", addr, "path", path)
		if err := httpServer.Start(addr); err != nil {
			slog.Error("mcp server stopped", "error", err)
			os.Exit(1)
		}
	default:
		log.Fatalf("unsupported MCP_TRANSPORT: %s", transport)
	}
}

func meshUp() error {
	if err := ensureNATS(); err != nil {
		return err
	}
	if err := ensureOpenCode(); err != nil {
		return err
	}
	if err := ensureRelayHTTP(); err != nil {
		return err
	}
	fmt.Println("mesh-up complete")
	fmt.Println("OpenCode URL: http://127.0.0.1:4097")
	fmt.Println("Relay MCP URL: http://127.0.0.1:8080/mcp")
	return nil
}

func meshDown() error {
	if err := stopManagedProcess("relay-http.pid"); err != nil {
		return err
	}
	stopRelayByPort()
	if err := stopManagedProcess("opencode-serve.pid"); err != nil {
		return err
	}
	_ = runCmd("docker", "rm", "-f", "relay-mesh-nats")
	fmt.Println("mesh-down complete")
	return nil
}

func ensureNATS() error {
	out, err := runCmdOutput("docker", "ps", "--filter", "name=^/relay-mesh-nats$", "--format", "{{.Names}}")
	if err != nil {
		return fmt.Errorf("check docker ps: %w", err)
	}
	if strings.TrimSpace(string(out)) == "relay-mesh-nats" {
		return nil
	}
	allOut, _ := runCmdOutput("docker", "ps", "-a", "--filter", "name=^/relay-mesh-nats$", "--format", "{{.Names}}")
	if strings.TrimSpace(string(allOut)) == "relay-mesh-nats" {
		if err := runCmd("docker", "start", "relay-mesh-nats"); err != nil {
			if natsReachable("127.0.0.1:4222") {
				slog.Warn("relay-mesh-nats could not start; reusing existing nats on 127.0.0.1:4222")
				return nil
			}
			return fmt.Errorf("start nats container: %w", err)
		}
		return nil
	}
	if err := runCmd("docker", "run", "-d", "--name", "relay-mesh-nats", "-p", "4222:4222", "nats:2.11-alpine", "-js"); err != nil {
		if natsReachable("127.0.0.1:4222") {
			slog.Warn("nats port already in use; reusing existing nats on 127.0.0.1:4222")
			return nil
		}
		return err
	}
	return nil
}

func ensureOpenCode() error {
	if httpReachable("http://127.0.0.1:4097/session") {
		return nil
	}
	logPath, pidPath, err := managedPaths("opencode-serve.log", "opencode-serve.pid")
	if err != nil {
		return err
	}
	return startDetached(
		"opencode",
		[]string{"serve", "--hostname", "127.0.0.1", "--port", "4097"},
		nil,
		logPath,
		pidPath,
		func() bool { return httpReachable("http://127.0.0.1:4097/session") },
	)
}

func ensureRelayHTTP() error {
	if relayServerReachable("http://127.0.0.1:8080/mcp") {
		return nil
	}
	if httpReachable("http://127.0.0.1:8080/") {
		return fmt.Errorf("port 8080 is in use by a non-relay service")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	logPath, pidPath, err := managedPaths("relay-http.log", "relay-http.pid")
	if err != nil {
		return err
	}
	env := []string{
		"NATS_URL=nats://127.0.0.1:4222",
		"OPENCODE_URL=http://127.0.0.1:4097",
		"MCP_TRANSPORT=http",
		"MCP_HTTP_ADDR=127.0.0.1:8080",
		"MCP_HTTP_PATH=/mcp",
	}
	return startDetached(
		exe,
		[]string{"serve"},
		env,
		logPath,
		pidPath,
		func() bool { return relayServerReachable("http://127.0.0.1:8080/mcp") },
	)
}

func httpReachable(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func natsReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func managedPaths(logName, pidName string) (string, string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, logName), filepath.Join(dir, pidName), nil
}

func stateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".relay-mesh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func startDetached(command string, args []string, extraEnv []string, logPath, pidPath string, check func() bool) error {
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()

	cmd := exec.Command(command, args...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		return err
	}

	for i := 0; i < 30; i++ {
		if check() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("process did not become ready: %s %s", command, strings.Join(args, " "))
}

func stopManagedProcess(pidFile string) error {
	dir, err := stateDir()
	if err != nil {
		return err
	}
	pidPath := filepath.Join(dir, pidFile)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	pidText := strings.TrimSpace(string(data))
	if pidText == "" {
		return nil
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
	_ = os.Remove(pidPath)
	return nil
}

func stopRelayByPort() {
	pids := pidsByListeningPort(8080)
	for _, pid := range pids {
		cmdline, err := processCommand(pid)
		if err != nil {
			continue
		}
		lc := strings.ToLower(cmdline)
		if strings.Contains(lc, "relay-mesh") || strings.Contains(lc, "cmd/server") {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Signal(syscall.SIGTERM)
			}
		}
	}
}

func relayServerReachable(mcpURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(mcpURL)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	// relay streamable MCP endpoint may return 200/400/405 depending on request shape.
	return resp.StatusCode != http.StatusNotFound
}

func pidsByListeningPort(port int) []int {
	out, err := runCmdOutput("lsof", "-nP", "-tiTCP:"+strconv.Itoa(port), "-sTCP:LISTEN")
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	pids := make([]int, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

func processCommand(pid int) (string, error) {
	out, err := runCmdOutput("ps", "-p", strconv.Itoa(pid), "-o", "command=")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCmdOutput(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.Output()
}

func installOpenCodePlugin() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	configPath := getenv("RELAY_MESH_OPENCODE_CONFIG", filepath.Join(home, ".config", "opencode", "opencode.json"))
	pluginPath := strings.TrimSpace(getenv("RELAY_MESH_PLUGIN_PATH", ""))
	if pluginPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		pluginPath = filepath.Join(cwd, ".opencode", "plugins", "relay-mesh-auto-bind.js")
	}
	if _, err := os.Stat(pluginPath); err != nil {
		return fmt.Errorf("plugin file not found: %s", pluginPath)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}

	cfg := map[string]any{}
	if data, err := os.ReadFile(configPath); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse %s: %w", configPath, err)
		}
	}

	pluginList := []any{}
	if raw, ok := cfg["plugin"]; ok {
		if arr, ok := raw.([]any); ok {
			pluginList = arr
		}
	}

	for _, v := range pluginList {
		if s, ok := v.(string); ok && s == pluginPath {
			fmt.Printf("OpenCode plugin already installed: %s\n", pluginPath)
			return nil
		}
	}

	pluginList = append(pluginList, pluginPath)
	cfg["plugin"] = pluginList

	// Keep existing file stable except for plugin insertion.
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	// Skip write when unchanged.
	if data, err := os.ReadFile(configPath); err == nil {
		var existing map[string]any
		if json.Unmarshal(data, &existing) == nil && reflect.DeepEqual(existing, cfg) {
			fmt.Printf("OpenCode plugin already installed: %s\n", pluginPath)
			return nil
		}
	}

	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("Installed OpenCode plugin into %s\n", configPath)
	fmt.Printf("Plugin path: %s\n", pluginPath)
	return nil
}

func buildMCPServer(b *broker.Broker, pusher *opencodepush.Pusher, resolver *opencodepush.SessionResolver) *server.MCPServer {
	s := server.NewMCPServer(
		"relay-mesh",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	registerTool := mcp.NewTool(
		"register_agent",
		mcp.WithDescription("Register an agent profile and return an agent_id."),
		mcp.WithString("name", mcp.Description("Optional display name for this agent.")),
		mcp.WithString("description", mcp.Required(), mcp.Description("Who this agent is and what they handle.")),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name/context for this agent.")),
		mcp.WithString("role", mcp.Required(), mcp.Description("Role in project (e.g., backend engineer).")),
		mcp.WithString("github", mcp.Description("GitHub handle/org this agent operates in.")),
		mcp.WithString("branch", mcp.Description("Current or primary git branch.")),
		mcp.WithString("specialization", mcp.Required(), mcp.Description("Primary specialization/skill domain.")),
		mcp.WithString("session_id", mcp.Description("Optional OpenCode session id to bind immediately.")),
	)
	listTool := mcp.NewTool(
		"list_agents",
		mcp.WithDescription("List all registered agents and their profiles."),
	)
	updateProfileTool := mcp.NewTool(
		"update_agent_profile",
		mcp.WithDescription("Update agent profile fields when new info becomes known."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("Agent id to update.")),
		mcp.WithString("name", mcp.Description("Updated display name.")),
		mcp.WithString("description", mcp.Description("Updated description.")),
		mcp.WithString("project", mcp.Description("Updated project.")),
		mcp.WithString("role", mcp.Description("Updated role.")),
		mcp.WithString("github", mcp.Description("Updated GitHub handle/org.")),
		mcp.WithString("branch", mcp.Description("Updated branch.")),
		mcp.WithString("specialization", mcp.Description("Updated specialization.")),
	)
	findAgentsTool := mcp.NewTool(
		"find_agents",
		mcp.WithDescription("Find relevant agents by query/profile filters."),
		mcp.WithString("query", mcp.Description("Free text search across profile fields.")),
		mcp.WithString("project", mcp.Description("Exact project filter.")),
		mcp.WithString("role", mcp.Description("Exact role filter.")),
		mcp.WithString("specialization", mcp.Description("Exact specialization filter.")),
		mcp.WithString("max", mcp.Description("Max number of agents to return (default 20).")),
	)
	sendTool := mcp.NewTool(
		"send_message",
		mcp.WithDescription("Send a message from one agent to another using NATS."),
		mcp.WithString("from", mcp.Required(), mcp.Description("Sender agent_id.")),
		mcp.WithString("to", mcp.Required(), mcp.Description("Recipient agent_id.")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Message body.")),
	)
	broadcastTool := mcp.NewTool(
		"broadcast_message",
		mcp.WithDescription("Broadcast a message to relevant agents using profile filters."),
		mcp.WithString("from", mcp.Required(), mcp.Description("Sender agent_id.")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Message body.")),
		mcp.WithString("query", mcp.Description("Free text search across profile fields.")),
		mcp.WithString("project", mcp.Description("Exact project filter.")),
		mcp.WithString("role", mcp.Description("Exact role filter.")),
		mcp.WithString("specialization", mcp.Description("Exact specialization filter.")),
		mcp.WithString("max", mcp.Description("Max recipients (default 20).")),
	)
	fetchTool := mcp.NewTool(
		"fetch_messages",
		mcp.WithDescription("Fetch pending messages for an agent."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("Agent id to fetch for.")),
		mcp.WithString("max", mcp.Description("Max number of messages to fetch (default 10).")),
	)
	fetchHistoryTool := mcp.NewTool(
		"fetch_message_history",
		mcp.WithDescription("Fetch durable JetStream message history for an agent without draining in-memory queue."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("Agent id to fetch history for.")),
		mcp.WithString("max", mcp.Description("Max number of historical messages to return (default 20).")),
	)
	bindSessionTool := mcp.NewTool(
		"bind_session",
		mcp.WithDescription("Bind an agent_id to an OpenCode session_id for automatic push delivery."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("Agent id to bind.")),
		mcp.WithString("session_id", mcp.Description("OpenCode session id. If omitted, server attempts to detect from request headers.")),
	)
	getBindingTool := mcp.NewTool(
		"get_session_binding",
		mcp.WithDescription("Get the currently bound OpenCode session for an agent_id."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("Agent id to resolve.")),
	)

	s.AddTool(registerTool, registerHandler(b, resolver))
	s.AddTool(listTool, listHandler(b))
	s.AddTool(updateProfileTool, updateProfileHandler(b))
	s.AddTool(findAgentsTool, findAgentsHandler(b))
	s.AddTool(sendTool, sendHandler(b, pusher))
	s.AddTool(broadcastTool, broadcastHandler(b, pusher))
	s.AddTool(fetchTool, fetchHandler(b))
	s.AddTool(fetchHistoryTool, fetchHistoryHandler(b))
	s.AddTool(bindSessionTool, bindSessionHandler(b))
	s.AddTool(getBindingTool, getSessionBindingHandler(b))
	return s
}

func registerHandler(b *broker.Broker, resolver *opencodepush.SessionResolver) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		profile := broker.AgentProfile{
			Name:           req.GetString("name", ""),
			Description:    req.GetString("description", ""),
			Project:        req.GetString("project", ""),
			Role:           req.GetString("role", ""),
			GitHub:         req.GetString("github", ""),
			Branch:         req.GetString("branch", ""),
			Specialization: req.GetString("specialization", ""),
		}
		id, err := b.RegisterAgent(profile)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		out := map[string]string{"agent_id": id}
		sessionID := strings.TrimSpace(req.GetString("session_id", ""))
		if sessionID == "" {
			sessionID = detectSessionID(req.Header)
		}
		if sessionID == "" && resolver != nil && resolver.Enabled() {
			bound := b.ListBoundSessionIDs()
			autoSessionID, resolveErr := resolver.FindLatestUnboundSession(bound)
			if resolveErr != nil {
				slog.Warn("auto bind resolver failed", "error", resolveErr)
			} else if autoSessionID != "" {
				sessionID = autoSessionID
			}
		}
		if sessionID != "" {
			if err := b.BindSession(id, sessionID); err == nil {
				out["session_id"] = sessionID
			}
		}
		body, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func updateProfileHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		agentID := req.GetString("agent_id", "")
		if agentID == "" {
			return mcp.NewToolResultError("agent_id is required"), nil
		}
		patch := broker.AgentProfile{
			Name:           req.GetString("name", ""),
			Description:    req.GetString("description", ""),
			Project:        req.GetString("project", ""),
			Role:           req.GetString("role", ""),
			GitHub:         req.GetString("github", ""),
			Branch:         req.GetString("branch", ""),
			Specialization: req.GetString("specialization", ""),
		}
		updated, err := b.UpdateAgentProfile(agentID, patch)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, _ := json.Marshal(updated)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func findAgentsHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		maxText := req.GetString("max", "20")
		max, err := strconv.Atoi(maxText)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid max: %s", maxText)), nil
		}
		filter := broker.AgentSearchFilter{
			Query:          req.GetString("query", ""),
			Project:        req.GetString("project", ""),
			Role:           req.GetString("role", ""),
			Specialization: req.GetString("specialization", ""),
			Limit:          max,
		}
		body, _ := json.Marshal(b.FindAgents(filter))
		return mcp.NewToolResultText(string(body)), nil
	}
}

func listHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		body, _ := json.Marshal(b.ListAgents())
		return mcp.NewToolResultText(string(body)), nil
	}
}

func sendHandler(b *broker.Broker, pusher *opencodepush.Pusher) server.ToolHandlerFunc {
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
		if pusher != nil && pusher.Enabled() {
			if sessionID, ok := b.GetSessionBinding(to); ok {
				if err := pusher.Push(sessionID, to, msg); err != nil {
					slog.Error("push delivery failed", "agent_id", to, "session_id", sessionID, "error", err)
				}
			}
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

func fetchHistoryHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		agentID := req.GetString("agent_id", "")
		if agentID == "" {
			return mcp.NewToolResultError("agent_id is required"), nil
		}

		maxText := req.GetString("max", "20")
		max, err := strconv.Atoi(maxText)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid max: %s", maxText)), nil
		}

		messages, err := b.FetchHistory(agentID, max)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, _ := json.Marshal(messages)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func broadcastHandler(b *broker.Broker, pusher *opencodepush.Pusher) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		from := req.GetString("from", "")
		bodyText := req.GetString("body", "")
		if from == "" || bodyText == "" {
			return mcp.NewToolResultError("from and body are required"), nil
		}

		maxText := req.GetString("max", "20")
		max, err := strconv.Atoi(maxText)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid max: %s", maxText)), nil
		}
		filter := broker.AgentSearchFilter{
			Query:          req.GetString("query", ""),
			Project:        req.GetString("project", ""),
			Role:           req.GetString("role", ""),
			Specialization: req.GetString("specialization", ""),
			Limit:          max,
		}

		messages, err := b.Broadcast(from, bodyText, filter)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if pusher != nil && pusher.Enabled() {
			for _, m := range messages {
				if sessionID, ok := b.GetSessionBinding(m.To); ok {
					if err := pusher.Push(sessionID, m.To, m); err != nil {
						slog.Error("push delivery failed", "agent_id", m.To, "session_id", sessionID, "error", err)
					}
				}
			}
		}
		body, _ := json.Marshal(messages)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func bindSessionHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		agentID := req.GetString("agent_id", "")
		if agentID == "" {
			return mcp.NewToolResultError("agent_id is required"), nil
		}

		sessionID := req.GetString("session_id", "")
		if strings.TrimSpace(sessionID) == "" {
			sessionID = detectSessionID(req.Header)
		}
		if strings.TrimSpace(sessionID) == "" {
			return mcp.NewToolResultError("session_id is required (or must be present in request headers)"), nil
		}

		if err := b.BindSession(agentID, sessionID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]string{
			"agent_id":   agentID,
			"session_id": sessionID,
		}
		body, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func getSessionBindingHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		agentID := req.GetString("agent_id", "")
		if agentID == "" {
			return mcp.NewToolResultError("agent_id is required"), nil
		}
		sessionID, ok := b.GetSessionBinding(agentID)
		if !ok {
			return mcp.NewToolResultError("no session bound for agent_id"), nil
		}
		out := map[string]string{
			"agent_id":   agentID,
			"session_id": sessionID,
		}
		body, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func detectSessionID(h http.Header) string {
	if h == nil {
		return ""
	}
	candidates := []string{
		"X-Opencode-Session-Id",
		"X-Opencode-SessionID",
		"X-Opencode-Session",
		"X-Session-Id",
		"X-Session-ID",
		"X-SessionID",
	}
	for _, k := range candidates {
		v := strings.TrimSpace(h.Get(k))
		if v != "" {
			return v
		}
	}
	return ""
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getDurationFromEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func getBoolFromEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		return fallback
	}
}
