import { readFile } from "node:fs/promises";
import type { ToolDefinition, ToolResult } from "@forge/types";

const IMAGE_EXTENSIONS = [".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg"];

function isImage(path: string): boolean {
  const lower = path.toLowerCase();
  return IMAGE_EXTENSIONS.some((ext) => lower.endsWith(ext));
}

function getMediaType(path: string): string {
  const lower = path.toLowerCase();
  if (lower.endsWith(".png")) return "image/png";
  if (lower.endsWith(".jpg") || lower.endsWith(".jpeg")) return "image/jpeg";
  if (lower.endsWith(".gif")) return "image/gif";
  if (lower.endsWith(".webp")) return "image/webp";
  if (lower.endsWith(".svg")) return "image/svg+xml";
  return "application/octet-stream";
}

export const readTool: ToolDefinition = {
  name: "Read",
  description:
    "Reads a file from the local filesystem. Supports line numbering (cat -n format), " +
    "offset (1-based line number to start), and limit (number of lines). " +
    "Detects image files (.png, .jpg, .jpeg, .gif, .webp, .svg) and returns base64.",
  inputSchema: {
    type: "object",
    properties: {
      file_path: {
        type: "string",
        description: "The absolute path to the file to read",
      },
      offset: {
        type: "number",
        description: "1-based line number to start reading from (default: 1)",
      },
      limit: {
        type: "number",
        description: "Number of lines to read (default: 2000)",
      },
    },
    required: ["file_path"],
  },
  annotations: { readOnly: true },
  handler: async (input): Promise<ToolResult> => {
    const filePath = input.file_path as string;
    const offset = (input.offset as number | undefined) ?? 1;
    const limit = (input.limit as number | undefined) ?? 2000;

    try {
      // Check if it's an image file
      if (isImage(filePath)) {
        const data = await readFile(filePath);
        const base64 = data.toString("base64");
        return {
          content: [
            {
              type: "image",
              source: {
                type: "base64",
                media_type: getMediaType(filePath),
                data: base64,
              },
            },
          ],
        };
      }

      // Read text file
      const content = await readFile(filePath, "utf-8");
      const lines = content.split("\n");

      // Apply offset and limit (offset is 1-based)
      const startIdx = offset - 1;
      const endIdx = startIdx + limit;
      const selectedLines = lines.slice(startIdx, endIdx);

      // Add line numbers (cat -n format: lineNum\tcontent)
      const numbered = selectedLines
        .map((line, idx) => `${startIdx + idx + 1}\t${line}`)
        .join("\n");

      return {
        content: [{ type: "text", text: numbered }],
      };
    } catch (err) {
      const error = err as NodeJS.ErrnoException;
      if (error.code === "ENOENT") {
        return {
          content: [
            {
              type: "text",
              text: `File does not exist: ${filePath}`,
            },
          ],
          isError: true,
        };
      }
      return {
        content: [
          {
            type: "text",
            text: `Failed to read file: ${error.message}`,
          },
        ],
        isError: true,
      };
    }
  },
};
