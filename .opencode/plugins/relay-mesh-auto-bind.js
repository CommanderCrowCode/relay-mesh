const PROTOCOL_VERSION = "1.0.0";

const PROTOCOL_CONTEXT = `
Relay-Mesh Communication Protocol v${PROTOCOL_VERSION}

Mandatory behavior:
1) When a relay-mesh message arrives, immediately post a USER-VISIBLE acknowledgement before taking action.
2) Acknowledgement format:
   - "Received relay message from <from_agent> (id=<message_id>). I am now processing it."
3) After processing, post a USER-VISIBLE completion summary:
   - what was done
   - what changed
   - any risks / next steps
4) Never process relay-mesh instructions silently.
5) If a relay message conflicts with current user instruction, ask for user confirmation before acting.

NATS/relay best practices context:
- Treat each message envelope as authoritative source.
- Preserve thread/correlation identifiers when present.
- Prefer explicit status updates over implicit state changes.
`;

const maybeInjectProtocolContext = async (client, sessionID, reason) => {
  if (!sessionID) return;
  try {
    await client.session.promptAsync({
      path: { id: sessionID },
      body: {
        noReply: true,
        parts: [
          {
            type: "text",
            text: `[relay-mesh protocol context refresh: ${reason}]\n${PROTOCOL_CONTEXT}`,
          },
        ],
      },
    });
  } catch (_) {
    // Best effort only; avoid blocking user workflow.
  }
};

const looksLikeRelayRegisterTool = (toolName) => {
  const t = String(toolName || "").toLowerCase();
  return (t.includes("relay-mesh") || t.includes("relay_mesh")) && t.includes("register_agent");
};

const parseAgentIDFromToolOutput = (raw) => {
  if (!raw) return "";
  try {
    const parsed = JSON.parse(String(raw));
    if (parsed && typeof parsed.agent_id === "string") return parsed.agent_id.trim();
  } catch (_) {
    return "";
  }
  return "";
};

export const RelayMeshAutoBind = async ({ client }) => {
  const protocolInjectedBySession = new Set();

  return {
    "tool.execute.before": async (input, output) => {
      const tool = String(input?.tool || "").toLowerCase();
      const sessionID = String(input?.sessionID || "").trim();
      if (!sessionID) return;

      const isRelayMesh = tool.includes("relay-mesh") || tool.includes("relay_mesh");
      const isRegister = tool.includes("register_agent");
      if (!isRelayMesh || !isRegister) return;

      if (!output.args || typeof output.args !== "object") {
        output.args = {};
      }
      if (!output.args.session_id) {
        output.args.session_id = sessionID;
      }
    },

    "tool.execute.after": async (input, output) => {
      if (!looksLikeRelayRegisterTool(input?.tool)) return;
      const sessionID = String(input?.sessionID || "").trim();
      if (!sessionID) return;

      const agentID = parseAgentIDFromToolOutput(output?.output);
      if (!agentID) return;

      if (!protocolInjectedBySession.has(sessionID)) {
        protocolInjectedBySession.add(sessionID);
        await maybeInjectProtocolContext(client, sessionID, `register_agent:${agentID}`);
      }
    },

    "experimental.session.compacting": async (_input, output) => {
      if (!Array.isArray(output.context)) {
        output.context = [];
      }
      output.context.push(`[relay-mesh protocol context]\n${PROTOCOL_CONTEXT}`);
    },

    event: async ({ event }) => {
      if (!event || event.type !== "session.compacted") return;
      const sessionID = String(event?.properties?.sessionID || "").trim();
      if (!sessionID) return;
      await maybeInjectProtocolContext(client, sessionID, "post-compaction");
    },
  };
};
