import type { SystemBlock, ContextBundle } from "@forge/types";
import type { ToolRegistry } from "@forge/tools";

export function assembleSystemPrompt(
  _context: ContextBundle,
  _tools: ToolRegistry,
  _cwd: string,
): SystemBlock[] {
  return [{ type: "text", text: "You are a coding assistant." }];
}
