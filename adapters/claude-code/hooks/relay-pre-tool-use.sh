#!/usr/bin/env bash
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

cat <<HOOKHOOKEOF
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow",
    "updatedInput": $UPDATED_INPUT
  }
}
HOOKEOF
