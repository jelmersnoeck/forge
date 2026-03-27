import type { SystemBlock, ContextBundle } from "@forge/types";
import type { ToolRegistry } from "@forge/tools";
import { platform } from "node:os";

const BASE_PROMPT = `You are a coding assistant with access to powerful tools. Your role is to help users write, edit, and understand code.

Core principles:
- Think carefully before acting
- Use tools to gather information before making changes
- Explain your reasoning when making decisions
- Ask for clarification when requirements are unclear
- Be concise and focused in your responses

You have access to file operations, code search, and command execution tools. Use them wisely.`;

export function assembleSystemPrompt(
  context: ContextBundle,
  tools: ToolRegistry,
  cwd: string,
): SystemBlock[] {
  const blocks: SystemBlock[] = [];

  // 1. Base coding agent prompt
  blocks.push({
    type: "text",
    text: BASE_PROMPT,
  });

  // 2. Environment info
  const envInfo = [
    `Working directory: ${cwd}`,
    `Platform: ${platform()}`,
    `Date: ${new Date().toISOString().split("T")[0]}`,
  ].join("\n");

  blocks.push({
    type: "text",
    text: envInfo,
  });

  // 3. CLAUDE.md content wrapped in system-reminder tags
  if (context.claudeMd.length > 0) {
    const claudeMdContent = context.claudeMd
      .map((entry) => `<!-- ${entry.path} (${entry.level}) -->\n${entry.content}`)
      .join("\n\n");

    blocks.push({
      type: "text",
      text: `<system-reminder>\n${claudeMdContent}\n</system-reminder>`,
      cacheControl: { type: "ephemeral" },
    });
  }

  // 4. Rules content wrapped in system-reminder tags
  if (context.rules.length > 0) {
    const rulesContent = context.rules
      .map((entry) => `<!-- ${entry.path} (${entry.level}) -->\n${entry.content}`)
      .join("\n\n");

    blocks.push({
      type: "text",
      text: `<system-reminder>\nRules:\n\n${rulesContent}\n</system-reminder>`,
      cacheControl: { type: "ephemeral" },
    });
  }

  // 5. Skill descriptions
  if (context.skillDescriptions.length > 0) {
    const skillsText = context.skillDescriptions
      .map((skill) => `- ${skill.name}: ${skill.description}`)
      .join("\n");

    blocks.push({
      type: "text",
      text: `Available skills:\n${skillsText}\n\nUse the Skill tool to invoke these when appropriate.`,
    });
  }

  // 6. Agent descriptions
  const agentNames = Object.keys(context.agentDefinitions);
  if (agentNames.length > 0) {
    const agentsText = agentNames
      .map((name) => {
        const agent = context.agentDefinitions[name];
        return `- ${name}: ${agent.description}`;
      })
      .join("\n");

    blocks.push({
      type: "text",
      text: `Available agents:\n${agentsText}\n\nUse the SpawnSubagent tool to delegate work to these agents when appropriate.`,
    });
  }

  return blocks;
}
