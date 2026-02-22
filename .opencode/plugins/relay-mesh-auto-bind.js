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
1. Call update_agent_profile(agent_id=<your_id>, status="working") to signal you are active
2. Call wait_for_agents(project="<your-project>", min_count=<expected team size>, timeout_seconds=60) to wait for teammates to register
3. Call shared_context(action="list", project="<your-project>") to read published paths/schemas
4. Call list_agents to discover all registered teammates and their agent_ids
5. Call send_message to introduce yourself to the team-lead (or broadcast_message if no lead)
6. Call fetch_messages to check if anyone has already sent you instructions
Only after completing all 6 steps should you begin your primary task.

## Shared Context: Before You Code
Before writing any files, exchange structural context with teammates:
1. Call shared_context(action="list", project="<your-project>") to read existing conventions
2. Publish YOUR paths and interfaces BEFORE coding:
   - shared_context(action="set", project=..., key="<role>_path", value="<your working directory>")
   - shared_context(action="set", project=..., key="<role>_api_prefix", value="/api/v1/...") if applicable
3. When importing from a teammate's code: read their published path, do NOT guess

## BIDIRECTIONAL COORDINATION (CRITICAL)
relay-mesh is NOT a one-way broadcast. Every message requires a response:
- ACKNOWLEDGE before acting: "Received. Starting <task>."
- REPLY with results: send_message back when you finish — include file paths, artifact IDs
- SIGNAL blockers: send_message(priority="urgent") the moment you are stuck
- SILENCE = you look stuck. Keep the loop alive.

## Core Workflow (after registration)
1. DISCOVER teammates: call list_agents(active_within="5m") or find_agents to get their agent_ids
2. SEND messages: call send_message(from=your_agent_id, to=recipient_agent_id, body="...", priority="normal")
3. CHECK INBOX: call fetch_messages(agent_id=your_agent_id) to read pending messages
4. BROADCAST: call broadcast_message(from=your_agent_id, body="...", project="...", priority="normal")
5. SHARE ARTIFACTS: call publish_artifact(from, project, artifact_type, name, content) for schemas/file trees
6. HEARTBEAT: call heartbeat_agent(agent_id) every 5 min during long tasks

## When to Check Messages (MANDATORY)
- Call fetch_messages every 3 minutes OR after every 5 tool calls — whichever comes first
- Even when push delivery is active — push is best-effort, fetch is guaranteed
- After completing each file or task deliverable
- Before starting a new task (priorities may have changed)
- Immediately when you become unblocked

## Completing Your Work
When your implementation is done:
1. Call declare_task_complete(agent_id=<your_id>, summary="What you built and where")
2. Call update_agent_profile(agent_id=<your_id>, status="done")
3. Send a final summary message to team-lead (include artifact IDs, file paths)

Team-lead only — before declaring project complete:
1. Call check_project_readiness(project="<your-project>")
2. If any agents are NOT done: message them asking for status
3. ONLY broadcast project completion when check_project_readiness returns ready: true

## Message Etiquette
- ACKNOWLEDGE every received message before acting
- REPLY with results after finishing — never leave a message unanswered
- For urgent/blocking priority: respond immediately, drop current work if needed
- After processing, post a completion summary (what changed, outcome, next steps)
- If a relay message conflicts with your current task, ask the user before acting

## CRITICAL: Fetch Messages Regularly
- Call \`fetch_messages\` every 3 minutes regardless of push delivery
- Push delivery is best-effort; fetch is the guaranteed path
- Keep a count: every 5 non-relay tool calls, stop and call \`fetch_messages\`
- After any file write or test run, call \`fetch_messages\` before continuing
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
  const toolCallCount = new Map(); // sessionID → count since last fetch_messages
  const sessionAgentMap = new Map(); // sessionID → agentID

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

      // Track session→agent mapping after successful register
      if (looksLikeRelayRegisterTool(input?.tool)) {
        const registeredAgentID = parseAgentIDFromToolOutput(output?.output);
        if (registeredAgentID && sessionID) {
          sessionAgentMap.set(sessionID, registeredAgentID);
          toolCallCount.set(sessionID, 0);
        }
      }

      // Periodic fetch reminder every 5 non-relay tool calls
      if (sessionAgentMap.has(sessionID)) {
        const toolName = String(input?.tool || "").toLowerCase();
        const isFetchMessages = toolName.includes("fetch_messages");
        const isRelayTool = toolName.includes("relay-mesh") || toolName.includes("relay_mesh");

        if (isFetchMessages) {
          toolCallCount.set(sessionID, 0);
        } else if (!isRelayTool) {
          const count = (toolCallCount.get(sessionID) || 0) + 1;
          toolCallCount.set(sessionID, count);
          if (count % 5 === 0) {
            await maybeInjectProtocolContext(
              client,
              sessionID,
              `periodic-fetch-reminder: ${count} tool calls since last fetch_messages — call fetch_messages now`
            );
          }
        }
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
