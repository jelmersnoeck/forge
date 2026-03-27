#!/usr/bin/env node
import { createInterface } from "node:readline";
import type { OutboundEvent } from "@forge/types";

const SERVER = process.env.FORGE_URL || "http://localhost:3000";

// ── HTTP helpers ────────────────────────────────────────────────

async function createThread(): Promise<string> {
  const res = await fetch(`${SERVER}/threads`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ metadata: { source: "cli" } }),
  });
  if (!res.ok) throw new Error(`failed to create thread: ${res.status}`);
  const { threadId } = (await res.json()) as { threadId: string };
  return threadId;
}

async function sendMessage(threadId: string, text: string): Promise<void> {
  const res = await fetch(`${SERVER}/threads/${threadId}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text, source: "cli" }),
  });
  if (!res.ok) throw new Error(`failed to send message: ${res.status}`);
}

// ── SSE listener ────────────────────────────────────────────────

function listenForEvents(
  threadId: string,
  onEvent: (event: OutboundEvent) => void,
): AbortController {
  const controller = new AbortController();

  (async () => {
    try {
      const res = await fetch(`${SERVER}/threads/${threadId}/events`, {
        signal: controller.signal,
      });

      const reader = res.body!.getReader();
      const decoder = new TextDecoder();
      let buffer = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const parts = buffer.split("\n\n");
        buffer = parts.pop()!;

        for (const part of parts) {
          for (const line of part.split("\n")) {
            if (!line.startsWith("data: ")) continue;
            try {
              onEvent(JSON.parse(line.slice(6)) as OutboundEvent);
            } catch {
              // malformed event — skip
            }
          }
        }
      }
    } catch (err) {
      if (err instanceof Error && err.name === "AbortError") return;
      console.error("event stream error:", err);
    }
  })();

  return controller;
}

// ── REPL ────────────────────────────────────────────────────────

async function main(): Promise<void> {
  let threadId: string;
  try {
    threadId = await createThread();
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    console.error(`could not connect to forge server at ${SERVER}`);
    console.error(`  ${msg}`);
    console.error(`\nhint: start the server with \`npm run dev:server\``);
    process.exit(1);
  }

  console.log(`forge cli — thread ${threadId.slice(0, 8)}`);
  console.log(`server: ${SERVER}\n`);

  const prompt = () => process.stdout.write("> ");

  const controller = listenForEvents(threadId, (event) => {
    switch (event.type) {
      case "text":
        if (event.content) {
          process.stdout.write(event.content);
          if (!event.content.endsWith("\n")) process.stdout.write("\n");
        }
        break;
      case "tool_use":
        console.log(`  [${event.toolName}]`);
        break;
      case "error":
        console.error(`error: ${event.content}`);
        break;
      case "done":
        console.log();
        prompt();
        break;
    }
  });

  const rl = createInterface({ input: process.stdin });
  prompt();

  rl.on("line", async (line) => {
    const text = line.trim();
    if (!text) {
      prompt();
      return;
    }
    try {
      await sendMessage(threadId, text);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      console.error(`send failed: ${msg}`);
      prompt();
    }
  });

  rl.on("close", () => {
    controller.abort();
    process.exit(0);
  });
}

main();
