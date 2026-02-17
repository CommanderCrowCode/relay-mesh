#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_DIR="$ROOT_DIR/.run"
mkdir -p "$RUN_DIR"
RELAY_BIN="$RUN_DIR/relay-server"

OPENCODE_HOST="${OPENCODE_HOST:-127.0.0.1}"
OPENCODE_PORT="${OPENCODE_PORT:-4097}"
OPENCODE_URL="${OPENCODE_URL:-http://${OPENCODE_HOST}:${OPENCODE_PORT}}"

MCP_HOST="${MCP_HOST:-127.0.0.1}"
MCP_PORT="${MCP_PORT:-8080}"
MCP_PATH="${MCP_PATH:-/mcp}"
MCP_URL="http://${MCP_HOST}:${MCP_PORT}${MCP_PATH}"
MCP_HEALTH_URL="http://${MCP_HOST}:${MCP_PORT}/"

wait_http() {
  local url="$1"
  local tries="${2:-30}"
  local delay="${3:-1}"
  local i
  for i in $(seq 1 "$tries"); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done
  return 1
}

is_mcp_up() {
  local code
  code="$(curl --max-time 2 -sS -o /dev/null -w "%{http_code}" "$MCP_HEALTH_URL" || true)"
  [[ "$code" != "000" && -n "$code" ]]
}

echo "[1/4] Starting NATS..."
docker compose -f "$ROOT_DIR/docker-compose.yml" up -d nats >/dev/null

echo "[2/4] Checking OpenCode server at ${OPENCODE_URL}..."
if curl -fsS "${OPENCODE_URL}/session" >/dev/null 2>&1; then
  echo "      OpenCode server already running."
else
  echo "      OpenCode server not found. Starting..."
  nohup opencode serve --hostname "$OPENCODE_HOST" --port "$OPENCODE_PORT" \
    >"$RUN_DIR/opencode-serve.log" 2>&1 &
  echo $! >"$RUN_DIR/opencode-serve.pid"
  if ! wait_http "${OPENCODE_URL}/session" 30 1; then
    echo "OpenCode server failed to start. See $RUN_DIR/opencode-serve.log"
    exit 1
  fi
  echo "      OpenCode server started."
fi

echo "[3/4] Checking relay MCP server at ${MCP_URL}..."
if is_mcp_up; then
  echo "      relay-mesh HTTP server already running."
else
  echo "      relay-mesh HTTP server not found. Starting..."
  go build -o "$RELAY_BIN" "$ROOT_DIR/cmd/server"
  nohup env \
    NATS_URL="${NATS_URL:-nats://127.0.0.1:4222}" \
    OPENCODE_URL="$OPENCODE_URL" \
    MCP_TRANSPORT=http \
    MCP_HTTP_ADDR="${MCP_HOST}:${MCP_PORT}" \
    MCP_HTTP_PATH="$MCP_PATH" \
    "$RELAY_BIN" \
    >"$RUN_DIR/relay-http.log" 2>&1 &
  echo $! >"$RUN_DIR/relay-http.pid"
  if ! is_mcp_up; then
    sleep 1
  fi
  if ! is_mcp_up; then
    echo "relay-mesh HTTP server failed to start. See $RUN_DIR/relay-http.log"
    exit 1
  fi
  echo "      relay-mesh HTTP server started."
fi

echo "[4/4] Done."
echo "OpenCode URL: ${OPENCODE_URL}"
echo "Relay MCP URL: ${MCP_URL}"
echo
echo "Next: in OpenCode, call register_agent (then bind_session only if session_id is not auto-returned)."
