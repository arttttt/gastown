// Gas Town OpenCode plugin: integrates with Gas Town multi-agent system.
// Based on opencode-beads pattern for context injection.

export const GasTown = async ({ client, $ }) => {
  const role = (process.env.GT_ROLE || "").toLowerCase();
  const autonomousRoles = new Set(["polecat", "witness", "refinery", "deacon"]);
  const injectedSessions = new Set();

  /**
   * Get the current model/agent context for a session.
   */
  async function getSessionContext(sessionID) {
    try {
      const response = await client.session.messages({
        path: { id: sessionID },
        query: { limit: 50 },
      });

      if (response.data) {
        for (const msg of response.data) {
          if (msg.info.role === "user" && "model" in msg.info && msg.info.model) {
            return { model: msg.info.model, agent: msg.info.agent };
          }
        }
      }
    } catch {
      // On error, return undefined
    }
    return undefined;
  }

  /**
   * Inject Gas Town context into a session.
   */
  async function injectGasTownContext(sessionID, context) {
    let contextParts = [];

    // Run gt prime and capture output
    try {
      const primeOutput = await $`gt prime`.text();
      if (primeOutput && primeOutput.trim()) {
        contextParts.push(`<gastown-context>\n${primeOutput.trim()}\n</gastown-context>`);
      }
    } catch {
      // Silent skip if gt prime fails
    }

    // For autonomous roles, also check mail
    if (autonomousRoles.has(role)) {
      try {
        const mailOutput = await $`gt mail check --inject`.text();
        if (mailOutput && mailOutput.trim()) {
          contextParts.push(`<gastown-mail>\n${mailOutput.trim()}\n</gastown-mail>`);
        }
      } catch {
        // Silent skip
      }
    }

    // Notify deacon of session start (fire-and-forget)
    $`gt nudge deacon session-started`.text().catch(() => {});

    // Inject collected context into session
    if (contextParts.length > 0) {
      const fullContext = contextParts.join("\n\n");
      try {
        await client.session.prompt({
          path: { id: sessionID },
          body: {
            noReply: true,
            model: context?.model,
            agent: context?.agent,
            parts: [{ type: "text", text: fullContext, synthetic: true }],
          },
        });
      } catch {
        // Silent skip
      }
    }
  }

  return {
    event: async ({ event }) => {
      if (event.type === "session.created") {
        const sessionID = event.properties?.info?.id || event.properties?.sessionID;
        if (sessionID && !injectedSessions.has(sessionID)) {
          injectedSessions.add(sessionID);
          await injectGasTownContext(sessionID, undefined);
        }
      }
      if (event.type === "session.compacted") {
        const sessionID = event.properties.sessionID;
        const context = await getSessionContext(sessionID);
        await injectGasTownContext(sessionID, context);
      }
      if (event.type === "session.deleted") {
        injectedSessions.delete(event.properties?.info?.id);
      }
    },

    // Customize compaction to preserve critical Gas Town context
    "experimental.session.compacting": async ({ sessionID }, output) => {
      const roleDisplay = role || "unknown";
      output.context.push(`
## Gas Town Multi-Agent System

You are working in the Gas Town multi-agent workspace.

**Critical Actions After Compaction:**
- Run \`gt prime\` to restore full Gas Town context
- Check your hook with \`gt mol status\` or \`gt hook\`
- If work is hooked, execute immediately per GUPP (Gas Town Universal Propulsion Principle)

**Current Session:**
- Role: ${roleDisplay}
- Session ID: ${sessionID}

**Remember:** The hook having work IS your assignment. Execute without waiting for confirmation.
`);
    },
  };
};
