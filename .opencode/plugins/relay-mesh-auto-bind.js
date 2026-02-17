export const RelayMeshAutoBind = async () => {
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
  };
};
