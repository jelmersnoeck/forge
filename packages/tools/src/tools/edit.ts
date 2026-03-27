import { readFile, writeFile } from "node:fs/promises";
import type { ToolDefinition, ToolResult } from "@forge/types";

export const editTool: ToolDefinition = {
  name: "Edit",
  description:
    "Performs exact string replacements in a file. The old_string must be unique " +
    "in the file (unless replace_all is true). Returns an error if old_string is " +
    "not found or appears multiple times without replace_all enabled.",
  inputSchema: {
    type: "object",
    properties: {
      file_path: {
        type: "string",
        description: "The absolute path to the file to edit",
      },
      old_string: {
        type: "string",
        description: "The text to replace (must be exact match)",
      },
      new_string: {
        type: "string",
        description: "The text to replace it with",
      },
      replace_all: {
        type: "boolean",
        description:
          "Replace all occurrences instead of requiring uniqueness (default: false)",
        default: false,
      },
    },
    required: ["file_path", "old_string", "new_string"],
  },
  annotations: { readOnly: false },
  handler: async (input): Promise<ToolResult> => {
    const filePath = input.file_path as string;
    const oldString = input.old_string as string;
    const newString = input.new_string as string;
    const replaceAll = (input.replace_all as boolean | undefined) ?? false;

    try {
      // Read file
      const content = await readFile(filePath, "utf-8");

      // Count occurrences
      const occurrences = countOccurrences(content, oldString);

      if (occurrences === 0) {
        return {
          content: [
            {
              type: "text",
              text: `String not found in file: "${oldString}"`,
            },
          ],
          isError: true,
        };
      }

      if (occurrences > 1 && !replaceAll) {
        return {
          content: [
            {
              type: "text",
              text:
                `String appears ${occurrences} times in file and is not unique. ` +
                `Use replace_all: true to replace all occurrences.`,
            },
          ],
          isError: true,
        };
      }

      // Perform replacement
      const newContent = replaceAll
        ? content.split(oldString).join(newString)
        : content.replace(oldString, newString);

      // Write back
      await writeFile(filePath, newContent, "utf-8");

      const message =
        occurrences === 1
          ? `Successfully replaced 1 occurrence in ${filePath}`
          : `Successfully replaced ${occurrences} occurrences in ${filePath}`;

      return {
        content: [{ type: "text", text: message }],
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
            text: `Failed to edit file: ${error.message}`,
          },
        ],
        isError: true,
      };
    }
  },
};

function countOccurrences(content: string, search: string): number {
  if (search.length === 0) return 0;
  let count = 0;
  let pos = 0;
  while ((pos = content.indexOf(search, pos)) !== -1) {
    count++;
    pos += search.length;
  }
  return count;
}
