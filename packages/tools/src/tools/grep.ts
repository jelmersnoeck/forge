import { spawn } from "node:child_process";
import type { ToolDefinition, ToolResult } from "@forge/types";

export const grepTool: ToolDefinition = {
  name: "Grep",
  description:
    "Search tool using ripgrep (rg). Supports regex patterns, file filters (glob), " +
    "and output modes: files_with_matches (default), content, count. " +
    "Optional flags: -i (case insensitive), -n (line numbers), -C (context lines), " +
    "head_limit (limit output lines).",
  inputSchema: {
    type: "object",
    properties: {
      pattern: {
        type: "string",
        description: "The regular expression pattern to search for",
      },
      path: {
        type: "string",
        description: "File or directory to search in (defaults to cwd)",
      },
      output_mode: {
        type: "string",
        enum: ["files_with_matches", "content", "count"],
        description:
          "Output mode: files_with_matches (file paths), content (matching lines), count (match counts)",
      },
      glob: {
        type: "string",
        description: "Glob pattern to filter files (e.g., '*.js', '*.{ts,tsx}')",
      },
      "-i": {
        type: "boolean",
        description: "Case insensitive search",
      },
      "-n": {
        type: "boolean",
        description: "Show line numbers (content mode only)",
      },
      "-C": {
        type: "number",
        description: "Show N lines of context (content mode only)",
      },
      context: {
        type: "number",
        description: "Alias for -C (context lines)",
      },
      head_limit: {
        type: "number",
        description: "Limit output to first N lines",
      },
    },
    required: ["pattern"],
  },
  annotations: { readOnly: true },
  handler: async (input, ctx): Promise<ToolResult> => {
    const pattern = input.pattern as string;
    const searchPath = (input.path as string | undefined) ?? ctx.cwd;
    const outputMode =
      (input.output_mode as string | undefined) ?? "files_with_matches";
    const glob = input.glob as string | undefined;
    const caseInsensitive = input["-i"] as boolean | undefined;
    const lineNumbers = input["-n"] as boolean | undefined;
    const contextLines =
      (input["-C"] as number | undefined) ??
      (input.context as number | undefined);
    const headLimit = input.head_limit as number | undefined;

    const args = [pattern, searchPath];

    // Output mode flags
    switch (outputMode) {
      case "files_with_matches":
        args.push("--files-with-matches");
        break;
      case "count":
        args.push("--count");
        break;
      // "content" is default ripgrep behavior
    }

    // Optional flags
    if (caseInsensitive) args.push("--ignore-case");
    if (lineNumbers && outputMode === "content") args.push("--line-number");
    if (contextLines && outputMode === "content") {
      args.push("--context", contextLines.toString());
    }
    if (glob) args.push("--glob", glob);

    return new Promise((resolve) => {
      const proc = spawn("rg", args, {
        cwd: ctx.cwd,
        signal: ctx.signal,
      });

      let stdout = "";
      let stderr = "";

      proc.stdout.on("data", (chunk) => {
        stdout += chunk.toString();
      });

      proc.stderr.on("data", (chunk) => {
        stderr += chunk.toString();
      });

      proc.on("error", (err) => {
        resolve({
          content: [
            {
              type: "text",
              text: `Failed to spawn ripgrep (rg): ${err.message}. Is ripgrep installed?`,
            },
          ],
          isError: true,
        });
      });

      proc.on("close", (code) => {
        // Exit code 1 means no matches (not an error)
        if (code === 1) {
          resolve({
            content: [{ type: "text", text: "(no matches)" }],
          });
          return;
        }

        // Other non-zero exit codes are errors
        if (code !== 0) {
          resolve({
            content: [{ type: "text", text: stderr || stdout }],
            isError: true,
          });
          return;
        }

        // Apply head_limit if specified
        let output = stdout;
        if (headLimit && headLimit > 0) {
          const lines = output.split("\n");
          output = lines.slice(0, headLimit).join("\n");
        }

        resolve({
          content: [{ type: "text", text: output }],
        });
      });
    });
  },
};
