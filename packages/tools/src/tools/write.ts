import { writeFile, mkdir } from "node:fs/promises";
import { dirname } from "node:path";
import type { ToolDefinition, ToolResult } from "@forge/types";

export const writeTool: ToolDefinition = {
  name: "Write",
  description:
    "Writes content to a file at the specified path. Creates parent directories " +
    "if they don't exist. Overwrites the file if it already exists.",
  inputSchema: {
    type: "object",
    properties: {
      file_path: {
        type: "string",
        description: "The absolute path to the file to write",
      },
      content: {
        type: "string",
        description: "The content to write to the file",
      },
    },
    required: ["file_path", "content"],
  },
  annotations: { readOnly: false },
  handler: async (input): Promise<ToolResult> => {
    const filePath = input.file_path as string;
    const content = input.content as string;

    try {
      // Create parent directories if they don't exist
      const dir = dirname(filePath);
      await mkdir(dir, { recursive: true });

      // Write file
      await writeFile(filePath, content, "utf-8");

      const byteCount = Buffer.byteLength(content, "utf-8");
      return {
        content: [
          {
            type: "text",
            text: `Successfully wrote ${byteCount} bytes to ${filePath}`,
          },
        ],
      };
    } catch (err) {
      const error = err as Error;
      return {
        content: [
          {
            type: "text",
            text: `Failed to write file: ${error.message}`,
          },
        ],
        isError: true,
      };
    }
  },
};
