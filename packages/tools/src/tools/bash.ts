import { spawn } from "node:child_process";
import type { ToolDefinition, ToolResult } from "@forge/types";

export const bashTool: ToolDefinition = {
  name: "Bash",
  description:
    "Executes a bash command in the working directory. " +
    "Captures stdout and stderr. Timeout defaults to 120s, max 600s. " +
    "On timeout, sends SIGTERM then SIGKILL after 5s.",
  inputSchema: {
    type: "object",
    properties: {
      command: {
        type: "string",
        description: "The bash command to execute",
      },
      timeout: {
        type: "number",
        description:
          "Optional timeout in milliseconds (default: 120000, max: 600000)",
      },
      description: {
        type: "string",
        description: "Optional description of what this command does",
      },
    },
    required: ["command"],
  },
  annotations: { destructive: false },
  handler: async (input, ctx): Promise<ToolResult> => {
    const command = input.command as string;
    const timeout = Math.min(
      (input.timeout as number | undefined) ?? 120_000,
      600_000,
    );

    return new Promise((resolve) => {
      const proc = spawn("bash", ["-c", command], {
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

      const killTimer = setTimeout(() => {
        proc.kill("SIGTERM");
        setTimeout(() => {
          if (!proc.killed) {
            proc.kill("SIGKILL");
          }
        }, 5000);
      }, timeout);

      proc.on("error", (err) => {
        clearTimeout(killTimer);
        resolve({
          content: [
            { type: "text", text: `Failed to spawn bash: ${err.message}` },
          ],
          isError: true,
        });
      });

      proc.on("close", (code) => {
        clearTimeout(killTimer);
        const combined = stdout + stderr;

        resolve({
          content: [{ type: "text", text: combined }],
          ...(code !== 0 && { isError: true }),
        });
      });
    });
  },
};
