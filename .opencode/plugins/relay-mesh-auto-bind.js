const PROTOCOL_VERSION = "2.0.0";

const PROTOCOL_CONTEXT = `
relay-mesh: Agent-to-Agent Messaging via MCP Tools

IMPORTANT: relay-mesh tools are MCP tools in your tool list. Call them directly like any other tool.
Do NOT write scripts, CLI commands, or HTTP calls to interact with relay-mesh or NATS.

## AUTOMATIC: Register on session start
As your FIRST action, call register_agent with:
- description: what you do (infer from your prompt/context)
- project: the project name (infer from working directory or task)
- role: your role (infer from your instructions)
- specialization: your expertise (infer from context)
Save the returned agent_id — you need it for ALL subsequent relay-mesh calls. Do this BEFORE any other work.

## IMMEDIATE: After registration completes
Do these steps BEFORE starting any other work:
1. Call list_agents to discover all registered teammates and their agent_ids
2. If a team lead exists, call send_message to introduce yourself (e.g., "I'm [name], ready to work on [area]"). Otherwise call broadcast_message to announce your presence to all teammates
3. Call fetch_messages to check if anyone has already sent you work or instructions
Only after completing all 3 steps should you begin your primary task.

## Core Workflow (after registration)
1. DISCOVER teammates: call list_agents or find_agents(query="backend") to get their agent_ids
2. SEND messages: call send_message(from=your_agent_id, to=recipient_agent_id, body="...")
3. CHECK INBOX: call fetch_messages(agent_id=your_agent_id) to read pending messages
4. BROADCAST: call broadcast_message(from=your_agent_id, body="...", project="...") for group updates

## When to Check Messages
- After completing each task or deliverable
- Before starting a new task (in case priorities changed)
- When waiting for a teammate's work
- Do NOT call fetch_messages in a tight loop — once every few minutes is enough

## Message Etiquette
- When you receive a message, acknowledge it visibly before acting on it
- After processing, post a completion summary (what changed, outcome, next steps)
- If a relay message conflicts with your current task, ask the user before acting
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

    "experimental.chat.system.transform": async (_input, output) => {
      if (!Array.isArray(output.system)) {
        output.system = [];
      }
      output.system.push(`[relay-mesh protocol context]\n${PROTOCOL_CONTEXT}`);
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
