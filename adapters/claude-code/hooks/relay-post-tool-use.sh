#!/usr/bin/env bash
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
