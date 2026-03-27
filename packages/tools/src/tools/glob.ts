import fg from "fast-glob";
import type { ToolDefinition, ToolResult } from "@forge/types";

export const globTool: ToolDefinition = {
  name: "Glob",
  description:
    "Fast file pattern matching using glob patterns (e.g., '**/*.ts'). " +
    "Returns file paths sorted by modification time (newest first).",
  inputSchema: {
    type: "object",
    properties: {
      pattern: {
        type: "string",
        description: "The glob pattern to match files against",
      },
      path: {
        type: "string",
        description:
          "The directory to search in (defaults to current working directory)",
      },
    },
    required: ["pattern"],
  },
  annotations: { readOnly: true },
  handler: async (input, ctx): Promise<ToolResult> => {
    const pattern = input.pattern as string;
    const searchPath = (input.path as string | undefined) ?? ctx.cwd;

    try {
      const entries = await fg(pattern, {
        cwd: searchPath,
        dot: true,
        onlyFiles: true,
        stats: true,
        absolute: true,
      });

      if (entries.length === 0) {
        return {
          content: [{ type: "text", text: "(no matches)" }],
        };
      }

      // Sort by modification time (newest first)
      const sorted = entries.sort((a, b) => {
        const aTime = a.stats?.mtime?.getTime() ?? 0;
        const bTime = b.stats?.mtime?.getTime() ?? 0;
        return bTime - aTime;
      });

      // Extract just the paths
      const paths = sorted.map((entry) => entry.path).join("\n");

      return {
        content: [{ type: "text", text: paths }],
      };
    } catch (err) {
      const error = err as Error;
      return {
        content: [
          { type: "text", text: `Failed to glob: ${error.message}` },
        ],
        isError: true,
      };
    }
  },
};
