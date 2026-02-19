#!/usr/bin/env bash
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
