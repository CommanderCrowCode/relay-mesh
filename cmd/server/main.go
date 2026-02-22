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
	urlpkg "net/url"
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
	"github.com/tanwa/relay-mesh/internal/push"
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
	case "install-claude-code":
		if err := installClaudeCode(); err != nil {
			slog.Error("install-claude-code failed", "error", err)
			os.Exit(1)
		}
	case "uninstall-claude-code":
		if err := uninstallClaudeCode(); err != nil {
			slog.Error("uninstall-claude-code failed", "error", err)
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
		fmt.Fprintf(os.Stderr, "usage: relay-mesh [serve|up|down|install-claude-code|uninstall-claude-code|install-opencode-plugin|version]\n")
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
	registry := push.NewRegistry()
	opencodeURL := getenv("OPENCODE_URL", "")
	if opencodeURL != "" {
		registry.Register(push.NewOpenCodeAdapter(
			opencodeURL,
			getDurationFromEnv("OPENCODE_PUSH_TIMEOUT", 15*time.Second),
			getBoolFromEnv("OPENCODE_NO_REPLY", false),
		))
	}
	home, err := os.UserHomeDir()
	if err == nil {
		registry.Register(push.NewClaudeCodeAdapter(filepath.Join(home, ".relay-mesh", "claude-code")))
	}
	resolver := opencodepush.NewSessionResolver(
		opencodeURL,
		getDurationFromEnv("OPENCODE_PUSH_TIMEOUT", 15*time.Second),
		getDurationFromEnv("OPENCODE_AUTO_BIND_WINDOW", 15*time.Minute),
	)

	s := buildMCPServer(b, registry, resolver)

	transport := getenv("MCP_TRANSPORT", "stdio")
	switch transport {
	case "stdio":
		if err := server.ServeStdio(s); err != nil {
			slog.Error("mcp server stopped", "error", err)
			os.Exit(1)
		}
	case "http":
		addr := getenv("MCP_HTTP_ADDR", "127.0.0.1:18808")
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
	mcpURL, err := ensureRelayHTTP()
	if err != nil {
		return err
	}
	fmt.Println("mesh-up complete")
	fmt.Println("OpenCode URL: http://127.0.0.1:4097")
	fmt.Printf("Relay MCP URL: %s\n", mcpURL)
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

func ensureRelayHTTP() (string, error) {
	// Determine HTTP address: saved config > env var > auto-find free port.
	mcpURL := loadHTTPAddr()
	if mcpURL == "" {
		addr := getenv("MCP_HTTP_ADDR", "")
		path := getenv("MCP_HTTP_PATH", "/mcp")
		if addr != "" {
			mcpURL = "http://" + addr + path
		} else {
			port := findFreePort(18808)
			addr = fmt.Sprintf("127.0.0.1:%d", port)
			mcpURL = fmt.Sprintf("http://%s%s", addr, path)
		}
	}

	if relayServerReachable(mcpURL) {
		return mcpURL, nil
	}

	// Extract host:port from URL for the server bind address.
	parsed, err := urlpkg.Parse(mcpURL)
	if err != nil {
		return "", fmt.Errorf("parse relay URL: %w", err)
	}
	addr := parsed.Host
	path := parsed.Path
	if path == "" {
		path = "/mcp"
	}

	if httpReachable("http://" + addr + "/") {
		return "", fmt.Errorf("port %s is in use by a non-relay service", addr)
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	logPath, pidPath, err := managedPaths("relay-http.log", "relay-http.pid")
	if err != nil {
		return "", err
	}
	env := []string{
		"NATS_URL=nats://127.0.0.1:4222",
		"OPENCODE_URL=" + getenv("OPENCODE_URL", "http://127.0.0.1:4097"),
		"MCP_TRANSPORT=http",
		"MCP_HTTP_ADDR=" + addr,
		"MCP_HTTP_PATH=" + path,
	}
	err = startDetached(
		exe,
		[]string{"serve"},
		env,
		logPath,
		pidPath,
		func() bool { return relayServerReachable(mcpURL) },
	)
	if err != nil {
		return "", err
	}
	return mcpURL, nil
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

	pluginFound := false
	for _, v := range pluginList {
		if s, ok := v.(string); ok && s == pluginPath {
			pluginFound = true
			break
		}
	}
	if !pluginFound {
		pluginList = append(pluginList, pluginPath)
		cfg["plugin"] = pluginList
	}

	// Ensure instructions array includes RELAY_AGENT_INSTRUCTIONS.md
	instructionsPath := filepath.Join(filepath.Dir(pluginPath), "..", "..", "adapters", "RELAY_AGENT_INSTRUCTIONS.md")
	if absInstr, err := filepath.Abs(instructionsPath); err == nil {
		if _, err := os.Stat(absInstr); err == nil {
			instrList := []any{}
			if raw, ok := cfg["instructions"]; ok {
				if arr, ok := raw.([]any); ok {
					instrList = arr
				}
			}
			found := false
			for _, v := range instrList {
				if s, ok := v.(string); ok && s == absInstr {
					found = true
					break
				}
			}
			if !found {
				instrList = append(instrList, absInstr)
				cfg["instructions"] = instrList
			}
		}
	}

	// Ensure MCP server entry exists
	mcpMap := map[string]any{}
	if raw, ok := cfg["mcp"]; ok {
		if m, ok := raw.(map[string]any); ok {
			mcpMap = m
		}
	}
	if _, ok := mcpMap["relay-mesh"]; !ok {
		port := getenv("RELAY_MESH_HTTP_PORT", "18808")
		mcpMap["relay-mesh"] = map[string]any{
			"type":    "remote",
			"url":     fmt.Sprintf("http://127.0.0.1:%s/mcp", port),
			"enabled": true,
			"timeout": 15000,
		}
		cfg["mcp"] = mcpMap
	}

	// Keep existing file stable except for our additions.
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

// ---------------------------------------------------------------------------
// install-claude-code
// ---------------------------------------------------------------------------

// Embedded hook scripts for Claude Code integration.
const claudeHookPreToolUse = `#!/usr/bin/env bash
# relay-mesh PreToolUse hook for Claude Code
# Injects session_id into register_agent calls
set -euo pipefail

INPUT=$(cat)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // ""')

# Only act on register_agent
case "$TOOL_NAME" in
  *register_agent*) ;;
  *) exit 0 ;;
esac

SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // ""')
if [ -z "$SESSION_ID" ]; then
  exit 0
fi

# Check if session_id already set in tool input
EXISTING=$(echo "$INPUT" | jq -r '.tool_input.session_id // ""')
if [ -n "$EXISTING" ]; then
  exit 0
fi

# Inject session_id and set harness type
UPDATED_INPUT=$(echo "$INPUT" | jq --arg sid "$SESSION_ID" '.tool_input + {"session_id": $sid, "harness": "claude-code"}')

cat <<HOOKEOF
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow",
    "updatedInput": $UPDATED_INPUT
  }
}
HOOKEOF
`

const claudeHookPostToolUse = `#!/usr/bin/env bash
# relay-mesh PostToolUse hook for Claude Code
# Injects protocol context after successful register_agent
set -euo pipefail

INPUT=$(cat)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // ""')

case "$TOOL_NAME" in
  *register_agent*) ;;
  *) exit 0 ;;
esac

# Check if registration was successful (output contains agent_id)
TOOL_OUTPUT=$(echo "$INPUT" | jq -r '.tool_output // ""')
if ! echo "$TOOL_OUTPUT" | jq -e '.agent_id' >/dev/null 2>&1; then
  exit 0
fi

AGENT_ID=$(echo "$TOOL_OUTPUT" | jq -r '.agent_id')
PROTOCOL_FILE="$HOME/.relay-mesh/claude-code/RELAY_PROTOCOL.md"

if [ -f "$PROTOCOL_FILE" ]; then
  CONTEXT=$(cat "$PROTOCOL_FILE")
else
  CONTEXT="relay-mesh agent registered as $AGENT_ID. Use fetch_messages to check for incoming messages."
fi

# Output context as additional info for Claude
echo "$CONTEXT" >&2
exit 0
`

const claudeHookStop = `#!/usr/bin/env bash
# relay-mesh Stop hook for Claude Code
# Checks for pending messages before going idle
set -euo pipefail

PENDING_FILE="$HOME/.relay-mesh/claude-code/pending-messages.json"

if [ ! -f "$PENDING_FILE" ]; then
  exit 0
fi

# Read and check for unread messages
PENDING=$(cat "$PENDING_FILE")
COUNT=$(echo "$PENDING" | jq 'length // 0')

if [ "$COUNT" -gt 0 ]; then
  # Clear the file after reading
  echo "[]" > "$PENDING_FILE"

  # Exit 2 = block stop, stderr becomes feedback to Claude
  echo "You have $COUNT new relay-mesh message(s). Use fetch_messages with your agent_id to read them:" >&2
  echo "$PENDING" | jq -r '.[] | "  From: \(.from) | Message: \(.body | .[0:100])"' >&2
  exit 2
fi

exit 0
`

const claudeRelayProtocol = `# Relay-Mesh Protocol Context

You are connected to relay-mesh for agent-to-agent messaging. All tools below are MCP tools in your tool list -- call them directly.

## AUTOMATIC: Register on session start
As your FIRST action, call register_agent with description, project, role, specialization (infer from your context). Save the returned agent_id -- you need it for ALL subsequent calls. Do this BEFORE any other work.

## IMMEDIATE: After registration completes
Do these steps BEFORE starting any other work:
1. Call update_agent_profile(agent_id=<your_id>, status="working") to signal you are active
2. Call wait_for_agents(project="<your-project>", min_count=<expected team size>, timeout_seconds=60) to wait for teammates
3. Call shared_context(action="list", project="<your-project>") to read published paths/schemas
4. Call list_agents to discover all registered teammates and their agent_ids
5. Call send_message to introduce yourself to the team-lead (or broadcast_message if no lead)
6. Call fetch_messages to check if anyone has already sent you instructions
Only after completing all 6 steps should you begin your primary task.

## Shared Context: Before You Code
Before writing any files, exchange structural context:
1. Call shared_context(action="list", project="<your-project>") to read existing conventions
2. Publish YOUR paths before coding: shared_context(action="set", project=..., key="<role>_path", value="<your directory>")
3. When importing from a teammate's code: read their published path first, do NOT guess

## When to Check Messages (MANDATORY)
- Call fetch_messages every 3 minutes OR after every 5 tool calls -- whichever comes first
- Even when push delivery is active -- push is best-effort, fetch is guaranteed
- After completing each file or task deliverable
- Before starting a new task (priorities may have changed)

## Completing Your Work
1. Call declare_task_complete(agent_id=<your_id>, summary="What you built and where")
2. Call update_agent_profile(agent_id=<your_id>, status="done")
3. Send a final summary message to team-lead
Team-lead ONLY: call check_project_readiness(project=...) before broadcasting project complete.

## Tools Reference
- register_agent(description, project, role, specialization, name?, session_id?) -- register yourself
- list_agents() -- see all agents
- find_agents(query?, project?, role?, specialization?) -- fuzzy search
- send_message(from, to, body) -- direct message; response includes recipient_unread count
- broadcast_message(from, body, project?, query?) -- group message; warns if 0 recipients
- fetch_messages(agent_id, max?) -- drain inbox; response includes remaining count
- update_agent_profile(agent_id, status?) -- update profile; status: idle|working|blocked|done
- get_team_status(project?) -- all agents' status, last_seen, unread_messages
- shared_context(action, project, key?, value?) -- publish/read paths, schemas, API contracts
- wait_for_agents(project, min_count?, timeout_seconds?) -- wait for N teammates to register
- declare_task_complete(agent_id, summary?) -- mark your work done
- check_project_readiness(project) -- check if all agents are done (team-lead uses before closing)
- bind_session(agent_id, session_id?) -- bind for push delivery
- fetch_message_history(agent_id) -- durable message history

## Message Etiquette
1. Acknowledge received messages before acting
2. Post completion summaries after finishing work
3. Never process relay messages silently
`

func installClaudeCode() error {
	projectDir, transport, httpURL := parseClaudeCodeFlags()

	if err := installClaudeCodeMCP(projectDir, transport, httpURL); err != nil {
		return fmt.Errorf("mcp config: %w", err)
	}
	if err := installClaudeCodeHooks(projectDir); err != nil {
		return fmt.Errorf("hooks: %w", err)
	}
	if err := installClaudeCodeSettings(projectDir); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	if err := installClaudeCodeProtocol(); err != nil {
		return fmt.Errorf("protocol: %w", err)
	}

	// Save HTTP address for relay-mesh up to use.
	if transport == "http" {
		if err := saveHTTPAddr(httpURL); err != nil {
			slog.Warn("could not save HTTP address", "error", err)
		}
	}

	fmt.Println("Installed relay-mesh for Claude Code.")
	fmt.Println()
	switch transport {
	case "http":
		fmt.Printf("Transport: HTTP (%s)\n", httpURL)
		fmt.Println()
		fmt.Println("Next steps:")
		fmt.Println("  1. relay-mesh up        # start NATS + relay server")
		fmt.Println("  2. Open Claude Code sessions in this directory")
		fmt.Println("  Agents register automatically and can message each other.")
	default:
		fmt.Println("Transport: stdio (each Claude Code session spawns its own relay-mesh)")
		fmt.Println()
		fmt.Println("Next steps:")
		fmt.Println("  1. Start NATS:  docker run -d -p 4222:4222 nats:2.11-alpine -js")
		fmt.Println("     (or: relay-mesh up)")
		fmt.Println("  2. Open Claude Code sessions in this directory")
		fmt.Println("  Agents register automatically and can message each other.")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Uninstall Claude Code
// ---------------------------------------------------------------------------

func uninstallClaudeCode() error {
	projectDir := ""
	for _, arg := range os.Args[2:] {
		if v, ok := cutFlag(arg, "--project-dir"); ok {
			projectDir = v
		}
	}
	if projectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot determine working directory: %w", err)
		}
		projectDir = cwd
	}

	var errs []error

	if err := uninstallClaudeCodeMCP(projectDir); err != nil {
		errs = append(errs, fmt.Errorf("mcp config: %w", err))
	}
	if err := uninstallClaudeCodeHooks(projectDir); err != nil {
		errs = append(errs, fmt.Errorf("hooks: %w", err))
	}
	if err := uninstallClaudeCodeSettings(projectDir); err != nil {
		errs = append(errs, fmt.Errorf("settings: %w", err))
	}
	if err := uninstallClaudeCodeProtocol(); err != nil {
		errs = append(errs, fmt.Errorf("protocol: %w", err))
	}

	if len(errs) > 0 {
		for _, e := range errs {
			slog.Warn("uninstall issue", "error", e)
		}
		return fmt.Errorf("%d components had errors during uninstall", len(errs))
	}

	fmt.Println("Uninstalled relay-mesh from Claude Code.")
	fmt.Printf("Project: %s\n", projectDir)
	return nil
}

// uninstallClaudeCodeMCP removes the relay-mesh entry from .mcp.json.
// If no other servers remain, the file is deleted entirely.
func uninstallClaudeCodeMCP(projectDir string) error {
	mcpPath := filepath.Join(projectDir, ".mcp.json")

	data, err := os.ReadFile(mcpPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil // nothing to remove
	}
	if err != nil {
		return err
	}

	cfg := map[string]any{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", mcpPath, err)
	}

	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		return nil
	}

	if _, exists := servers["relay-mesh"]; !exists {
		return nil
	}

	delete(servers, "relay-mesh")

	// If no servers remain, delete the file.
	if len(servers) == 0 {
		return os.Remove(mcpPath)
	}

	cfg["mcpServers"] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(mcpPath, out, 0o644)
}

// uninstallClaudeCodeHooks removes relay-mesh hook scripts from .claude/hooks/.
func uninstallClaudeCodeHooks(projectDir string) error {
	hooksDir := filepath.Join(projectDir, ".claude", "hooks")
	scripts := []string{
		"relay-pre-tool-use.sh",
		"relay-post-tool-use.sh",
		"relay-stop.sh",
	}
	for _, name := range scripts {
		path := filepath.Join(hooksDir, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// uninstallClaudeCodeSettings removes relay-mesh hook entries from .claude/settings.json.
// Preserves all non-relay-mesh hook entries and other settings.
func uninstallClaudeCodeSettings(projectDir string) error {
	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")

	data, err := os.ReadFile(settingsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	cfg := map[string]any{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", settingsPath, err)
	}

	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}

	changed := false
	for event, raw := range hooks {
		arr, ok := raw.([]any)
		if !ok {
			continue
		}
		filtered := filterOutRelayHooks(arr)
		if len(filtered) != len(arr) {
			changed = true
			if len(filtered) == 0 {
				delete(hooks, event)
			} else {
				hooks[event] = filtered
			}
		}
	}

	if !changed {
		return nil
	}

	// If hooks map is now empty, remove it from config.
	if len(hooks) == 0 {
		delete(cfg, "hooks")
	} else {
		cfg["hooks"] = hooks
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(settingsPath, out, 0o644)
}

// filterOutRelayHooks returns hook entries that don't reference relay-mesh scripts.
func filterOutRelayHooks(arr []any) []any {
	var result []any
	for _, raw := range arr {
		obj, ok := raw.(map[string]any)
		if !ok {
			result = append(result, raw)
			continue
		}
		hookList, _ := obj["hooks"].([]any)
		isRelay := false
		for _, h := range hookList {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hm["command"].(string)
			if strings.Contains(cmd, ".claude/hooks/relay-") {
				isRelay = true
				break
			}
		}
		if !isRelay {
			result = append(result, raw)
		}
	}
	return result
}

// uninstallClaudeCodeProtocol removes ~/.relay-mesh/claude-code/RELAY_PROTOCOL.md.
func uninstallClaudeCodeProtocol() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".relay-mesh", "claude-code", "RELAY_PROTOCOL.md")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// saveHTTPAddr persists the HTTP URL so relay-mesh up can use it.
func saveHTTPAddr(httpURL string) error {
	dir, err := stateDir()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "http-addr"), []byte(httpURL+"\n"), 0o644)
}

// loadHTTPAddr reads a previously saved HTTP address, or returns empty string.
func loadHTTPAddr() string {
	dir, err := stateDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "http-addr"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// parseClaudeCodeFlags extracts --project-dir, --transport, and --http-url
// from os.Args using the same manual style as the rest of the codebase.
func parseClaudeCodeFlags() (projectDir, transport, httpURL string) {
	projectDir = ""
	transport = "stdio"
	httpURL = "" // auto-detect free port when empty

	for _, arg := range os.Args[2:] {
		if v, ok := cutFlag(arg, "--project-dir"); ok {
			projectDir = v
		} else if v, ok := cutFlag(arg, "--transport"); ok {
			transport = v
		} else if v, ok := cutFlag(arg, "--http-url"); ok {
			httpURL = v
		}
	}

	if projectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			slog.Error("cannot determine working directory", "error", err)
			os.Exit(1)
		}
		projectDir = cwd
	}

	// Auto-find a free port for HTTP transport when no URL specified.
	if transport == "http" && httpURL == "" {
		port := findFreePort(18808)
		httpURL = fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	}
	return
}

// findFreePort probes ports starting from startPort and returns the first free one.
func findFreePort(startPort int) int {
	for port := startPort; port < startPort+100; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		ln.Close()
		return port
	}
	return startPort // fallback
}

// cutFlag returns the value for "--key=value" or "--key value" style flags.
func cutFlag(arg, prefix string) (string, bool) {
	if arg == prefix {
		// No value provided inline; caller would need next arg, but for
		// simplicity we only support --key=value form.
		return "", false
	}
	if strings.HasPrefix(arg, prefix+"=") {
		return arg[len(prefix)+1:], true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// 3a. .mcp.json
// ---------------------------------------------------------------------------

func installClaudeCodeMCP(projectDir, transport, httpURL string) error {
	mcpPath := filepath.Join(projectDir, ".mcp.json")

	cfg := map[string]any{}
	if data, err := os.ReadFile(mcpPath); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse %s: %w", mcpPath, err)
		}
	}

	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	var entry map[string]any
	switch transport {
	case "http":
		entry = map[string]any{
			"type": "http",
			"url":  httpURL,
		}
	default: // stdio
		entry = map[string]any{
			"command": "relay-mesh",
			"args":    []any{"serve"},
			"env": map[string]any{
				"NATS_URL": "nats://127.0.0.1:4222",
			},
		}
	}

	servers["relay-mesh"] = entry
	cfg["mcpServers"] = servers

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(mcpPath, out, 0o644)
}

// ---------------------------------------------------------------------------
// 3b. Hook scripts in .claude/hooks/
// ---------------------------------------------------------------------------

func installClaudeCodeHooks(projectDir string) error {
	hooksDir := filepath.Join(projectDir, ".claude", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}

	scripts := map[string]string{
		"relay-pre-tool-use.sh":  claudeHookPreToolUse,
		"relay-post-tool-use.sh": claudeHookPostToolUse,
		"relay-stop.sh":          claudeHookStop,
	}
	for name, content := range scripts {
		path := filepath.Join(hooksDir, name)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 3c. Protocol file in ~/.relay-mesh/claude-code/
// ---------------------------------------------------------------------------

func installClaudeCodeProtocol() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".relay-mesh", "claude-code")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "RELAY_PROTOCOL.md"), []byte(claudeRelayProtocol), 0o644)
}

// ---------------------------------------------------------------------------
// 3d. .claude/settings.json — merge hook entries
// ---------------------------------------------------------------------------

func installClaudeCodeSettings(projectDir string) error {
	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}

	cfg := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse %s: %w", settingsPath, err)
		}
	}

	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	// Define desired hook entries.
	type hookEntry struct {
		Matcher string `json:"matcher,omitempty"`
		Hooks   []any  `json:"hooks"`
	}

	wantedHooks := map[string]hookEntry{
		"PreToolUse": {
			Matcher: "mcp__relay-mesh__register_agent",
			Hooks:   []any{map[string]any{"type": "command", "command": ".claude/hooks/relay-pre-tool-use.sh"}},
		},
		"PostToolUse": {
			Matcher: "mcp__relay-mesh__register_agent",
			Hooks:   []any{map[string]any{"type": "command", "command": ".claude/hooks/relay-post-tool-use.sh"}},
		},
		"Stop": {
			Matcher: "",
			Hooks:   []any{map[string]any{"type": "command", "command": ".claude/hooks/relay-stop.sh"}},
		},
	}

	for event, wanted := range wantedHooks {
		arr, _ := hooks[event].([]any)

		newEntry := map[string]any{
			"hooks": wanted.Hooks,
		}
		if wanted.Matcher != "" {
			newEntry["matcher"] = wanted.Matcher
		}

		if !hookEntryExists(arr, wanted.Matcher, ".claude/hooks/relay-") {
			arr = append(arr, newEntry)
			hooks[event] = arr
		}
	}

	cfg["hooks"] = hooks

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(settingsPath, out, 0o644)
}

// hookEntryExists checks whether the hook array already contains a relay-mesh entry,
// identified by matching the matcher field and any hook command containing cmdSubstr.
func hookEntryExists(arr []any, matcher, cmdSubstr string) bool {
	for _, raw := range arr {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		// Check matcher matches (or both are empty)
		m, _ := obj["matcher"].(string)
		if m != matcher {
			continue
		}
		// Check if any hook command contains the relay substring
		hookList, _ := obj["hooks"].([]any)
		for _, h := range hookList {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hm["command"].(string)
			if strings.Contains(cmd, cmdSubstr) {
				return true
			}
		}
	}
	return false
}

func buildMCPServer(b *broker.Broker, registry *push.Registry, resolver *opencodepush.SessionResolver) *server.MCPServer {
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
		mcp.WithString("session_id", mcp.Description("Optional session id to bind immediately (auto-detected via hooks).")),
		mcp.WithString("harness", mcp.Description("Harness type: opencode, claude-code, codex, generic. Auto-detected if omitted.")),
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
		mcp.WithString("status", mcp.Description("Agent status: idle, working, blocked, or done.")),
	)
	findAgentsTool := mcp.NewTool(
		"find_agents",
		mcp.WithDescription("Find relevant agents by query/profile filters."),
		mcp.WithString("query", mcp.Description("Free text search across profile fields.")),
		mcp.WithString("project", mcp.Description("Project filter (fuzzy matching).")),
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
		mcp.WithDescription("Bind an agent_id to a harness session for automatic push delivery."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("Agent id to bind.")),
		mcp.WithString("session_id", mcp.Description("Session id. If omitted, server attempts to detect from request headers.")),
		mcp.WithString("harness", mcp.Description("Harness type: opencode, claude-code, codex, generic. Auto-detected if omitted.")),
	)
	getBindingTool := mcp.NewTool(
		"get_session_binding",
		mcp.WithDescription("Get the currently bound session and harness for an agent_id."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("Agent id to resolve.")),
	)
	getTeamStatusTool := mcp.NewTool(
		"get_team_status",
		mcp.WithDescription("Get current status of all agents on a project (idle/working/blocked/done), last activity, and unread message count. Call before declaring project complete."),
		mcp.WithString("project", mcp.Description("Project name filter. Leave empty to return all agents.")),
	)
	sharedContextTool := mcp.NewTool(
		"shared_context",
		mcp.WithDescription("Publish or read shared key-value context visible to all agents on the project. Use to share file paths, API endpoints, and schemas before coding. Read before importing to avoid path mismatches."),
		mcp.WithString("action", mcp.Required(), mcp.Description("Action: set, get, or list.")),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name.")),
		mcp.WithString("key", mcp.Description("Key to set or get (required for set/get).")),
		mcp.WithString("value", mcp.Description("Value to store (for set). Empty string deletes the key.")),
	)
	waitForAgentsTool := mcp.NewTool(
		"wait_for_agents",
		mcp.WithDescription("Wait until min_count agents have registered for a project. Call right after registering, before your first broadcast, to prevent 0-recipient race conditions."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name to watch.")),
		mcp.WithString("min_count", mcp.Description("Minimum number of agents to wait for (default 2).")),
		mcp.WithString("timeout_seconds", mcp.Description("Max seconds to wait (default 60).")),
	)
	declareCompleteTool := mcp.NewTool(
		"declare_task_complete",
		mcp.WithDescription("Declare that your assigned work is complete. Sets your status to 'done' so the team-lead can track overall progress."),
		mcp.WithString("agent_id", mcp.Required(), mcp.Description("Your agent_id.")),
		mcp.WithString("summary", mcp.Description("Brief summary of what you completed.")),
	)
	checkReadinessTool := mcp.NewTool(
		"check_project_readiness",
		mcp.WithDescription("Check whether all agents on a project have declared completion. Team-lead MUST call this before broadcasting project complete."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name to check.")),
	)

	s.AddTool(registerTool, registerHandler(b, resolver))
	s.AddTool(listTool, listHandler(b))
	s.AddTool(updateProfileTool, updateProfileHandler(b))
	s.AddTool(findAgentsTool, findAgentsHandler(b))
	s.AddTool(sendTool, sendHandler(b, registry))
	s.AddTool(broadcastTool, broadcastHandler(b, registry))
	s.AddTool(fetchTool, fetchHandler(b))
	s.AddTool(fetchHistoryTool, fetchHistoryHandler(b))
	s.AddTool(bindSessionTool, bindSessionHandler(b))
	s.AddTool(getBindingTool, getSessionBindingHandler(b))
	s.AddTool(getTeamStatusTool, getTeamStatusHandler(b))
	s.AddTool(sharedContextTool, sharedContextHandler(b))
	s.AddTool(waitForAgentsTool, waitForAgentsHandler(b))
	s.AddTool(declareCompleteTool, declareCompleteHandler(b))
	s.AddTool(checkReadinessTool, checkReadinessHandler(b))
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

		// Detect harness type
		harness := strings.TrimSpace(req.GetString("harness", ""))
		if harness == "" {
			harness = detectHarness()
		}

		// Detect session_id EARLY (before register call)
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

		var id string
		var created bool
		if sessionID != "" {
			var err error
			id, created, err = b.RegisterOrUpdateBySession(sessionID, profile)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		} else {
			var err error
			id, err = b.RegisterAgent(profile)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			created = true
		}

		slog.Info("agent registered", "agent_id", id, "new", created, "name", profile.Name, "project", profile.Project, "role", profile.Role)

		out := map[string]string{"agent_id": id}
		if sessionID != "" {
			// RegisterOrUpdateBySession already binds the session internally,
			// but we still need to set the harness via BindSession.
			if err := b.BindSession(id, sessionID, harness); err == nil {
				out["session_id"] = sessionID
				out["harness"] = harness
			}
		} else if harness != "" {
			out["harness"] = harness
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
			Status:         req.GetString("status", ""),
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

func sendHandler(b *broker.Broker, registry *push.Registry) server.ToolHandlerFunc {
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
		slog.Info("message sent", "id", msg.ID, "from", from, "to", to, "body", msgBody)
		if registry != nil {
			if sessionID, harness, ok := b.GetSessionBindingWithHarness(to); ok && harness != "generic" {
				pushMsg := push.Message{
					ID:        msg.ID,
					From:      msg.From,
					To:        msg.To,
					Body:      msg.Body,
					CreatedAt: msg.CreatedAt.Format(time.RFC3339),
				}
				if err := registry.Push(harness, sessionID, to, pushMsg); err != nil {
					slog.Error("push delivery failed", "agent_id", to, "harness", harness, "error", err)
				}
			}
		}
		out := map[string]any{
			"id":               msg.ID,
			"from":             msg.From,
			"to":               msg.To,
			"body":             msg.Body,
			"created_at":       msg.CreatedAt,
			"recipient_unread": b.UnreadCount(to),
		}
		body, _ := json.Marshal(out)
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
		for _, m := range messages {
			slog.Info("message delivered", "agent_id", agentID, "id", m.ID, "from", m.From, "body", m.Body)
		}
		out := map[string]any{
			"messages":  messages,
			"count":     len(messages),
			"remaining": b.UnreadCount(agentID),
		}
		body, _ := json.Marshal(out)
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

func broadcastHandler(b *broker.Broker, registry *push.Registry) server.ToolHandlerFunc {
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
		slog.Info("broadcast sent", "from", from, "recipients", len(messages), "body", bodyText)

		if len(messages) == 0 {
			body, _ := json.Marshal(map[string]any{
				"status":     "warning",
				"message":    "Broadcast sent but reached 0 recipients. No agents match your filters yet. Call wait_for_agents first, or retry after teammates have registered.",
				"recipients": 0,
			})
			return mcp.NewToolResultText(string(body)), nil
		}

		if registry != nil {
			for _, m := range messages {
				if sessionID, harness, ok := b.GetSessionBindingWithHarness(m.To); ok && harness != "generic" {
					pushMsg := push.Message{
						ID:        m.ID,
						From:      m.From,
						To:        m.To,
						Body:      m.Body,
						CreatedAt: m.CreatedAt.Format(time.RFC3339),
					}
					if err := registry.Push(harness, sessionID, m.To, pushMsg); err != nil {
						slog.Warn("broadcast push delivery failed", "from", from, "to", m.To, "harness", harness, "error", err)
					} else {
						slog.Info("broadcast push delivered", "from", from, "to", m.To, "harness", harness)
					}
				}
			}
		}
		out := map[string]any{
			"status":     "ok",
			"recipients": len(messages),
			"messages":   messages,
		}
		body, _ := json.Marshal(out)
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

		harness := strings.TrimSpace(req.GetString("harness", ""))
		if harness == "" {
			harness = detectHarness()
		}

		if err := b.BindSession(agentID, sessionID, harness); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		slog.Info("session bound", "agent_id", agentID, "session_id", sessionID, "harness", harness)
		out := map[string]string{
			"agent_id":   agentID,
			"session_id": sessionID,
			"harness":    harness,
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
		sessionID, harness, ok := b.GetSessionBindingWithHarness(agentID)
		if !ok {
			return mcp.NewToolResultError("no session bound for agent_id"), nil
		}
		out := map[string]string{
			"agent_id":   agentID,
			"session_id": sessionID,
			"harness":    harness,
		}
		body, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func getTeamStatusHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := req.GetString("project", "")
		statuses := b.GetTeamStatus(project)
		body, _ := json.Marshal(statuses)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func sharedContextHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		action := strings.TrimSpace(req.GetString("action", ""))
		project := req.GetString("project", "")
		key := req.GetString("key", "")
		value := req.GetString("value", "")

		switch action {
		case "set":
			if err := b.SharedContextSet(project, key, value); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out := map[string]any{"ok": true, "project": project, "key": key, "value": value}
			body, _ := json.Marshal(out)
			return mcp.NewToolResultText(string(body)), nil
		case "get":
			v, found := b.SharedContextGet(project, key)
			out := map[string]any{"found": found, "value": v}
			body, _ := json.Marshal(out)
			return mcp.NewToolResultText(string(body)), nil
		case "list":
			m := b.SharedContextList(project)
			body, _ := json.Marshal(m)
			return mcp.NewToolResultText(string(body)), nil
		default:
			return mcp.NewToolResultError("action must be set, get, or list"), nil
		}
	}
}

func waitForAgentsHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := req.GetString("project", "")
		if project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}
		minCount := 2
		if s := req.GetString("min_count", ""); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				minCount = n
			}
		}
		timeoutSec := 60
		if s := req.GetString("timeout_seconds", ""); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				timeoutSec = n
			}
		}
		agents, met := b.WaitForAgents(project, minCount, timeoutSec)
		out := map[string]any{
			"met":    met,
			"count":  len(agents),
			"agents": agents,
		}
		body, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func declareCompleteHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		agentID := req.GetString("agent_id", "")
		if agentID == "" {
			return mcp.NewToolResultError("agent_id is required"), nil
		}
		summary := req.GetString("summary", "")
		if _, err := b.UpdateAgentProfile(agentID, broker.AgentProfile{Status: "done"}); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		slog.Info("agent declared task complete", "agent_id", agentID, "summary", summary)
		out := map[string]any{"ok": true, "agent_id": agentID, "status": "done", "summary": summary}
		body, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func checkReadinessHandler(b *broker.Broker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project := req.GetString("project", "")
		if project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}
		statuses := b.GetTeamStatus(project)
		doneCount := 0
		type pendingEntry struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		pending := make([]pendingEntry, 0)
		for _, s := range statuses {
			if s.Status == "done" {
				doneCount++
			} else {
				pending = append(pending, pendingEntry{ID: s.ID, Name: s.Name, Status: s.Status})
			}
		}
		out := map[string]any{
			"ready":          len(pending) == 0 && len(statuses) > 0,
			"total_agents":   len(statuses),
			"done_count":     doneCount,
			"pending_agents": pending,
		}
		body, _ := json.Marshal(out)
		return mcp.NewToolResultText(string(body)), nil
	}
}

func detectHarness() string {
	if os.Getenv("CODEX_THREAD_ID") != "" {
		return "codex"
	}
	// Claude Code and OpenCode don't set obvious env vars when running MCP
	// servers; default to "generic" and let hooks/explicit binding set it.
	return "generic"
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
