import { query } from "@anthropic-ai/claude-agent-sdk";
import { randomUUID } from "node:crypto";
import { getThread, setThread, pullMessage, publishEvent } from "./bus.js";
import { config } from "./config.js";
import type { OutboundEvent, ThreadMeta } from "@forge/types";

// startWorker runs the core agent loop for a single thread.
//
//   pullMessage (blocks)
//       │
//       ▼
//   query({ prompt, resume? })
//       │
//       ├──▶ stream SDKMessages → publishEvent
//       │
//       ▼
//   persist sessionId via setThread
//       │
//       ▼
//   loop back to pullMessage
//
export async function startWorker(threadId: string): Promise<void> {
  const meta: ThreadMeta = getThread(threadId) ?? {
    threadId,
    metadata: {},
    createdAt: Date.now(),
    lastActiveAt: Date.now(),
  };
  let sessionId = meta.sessionId;

  const emit = (event: Omit<Partial<OutboundEvent>, "id" | "threadId" | "timestamp">): void => {
    publishEvent(threadId, {
      id: randomUUID(),
      threadId,
      type: "text",
      timestamp: Date.now(),
      ...event,
    });
  };

  console.log(`[worker:${threadId}] started, session=${sessionId ?? "new"}`);

  while (true) {
    const msg = await pullMessage(threadId);
    console.log(`[worker:${threadId}] ← ${msg.text.slice(0, 100)}`);

    try {
      const q = query({
        prompt: msg.text,
        options: {
          tools: { type: "preset", preset: "claude_code" },
          systemPrompt: { type: "preset", preset: "claude_code" },
          permissionMode: "bypassPermissions",
          allowDangerouslySkipPermissions: true,
          cwd: config.worker.workspaceDir,
          ...(sessionId ? { resume: sessionId } : {}),
        },
      });

      for await (const message of q) {
        switch (message.type) {
          case "system":
            if (message.subtype === "init") {
              sessionId = message.session_id;
            }
            break;

          case "assistant":
            if (message.message?.content) {
              for (const block of message.message.content) {
                if ("text" in block && block.text) {
                  emit({ type: "text", content: block.text });
                }
                if ("name" in block) {
                  emit({ type: "tool_use", toolName: (block as { name: string }).name });
                }
              }
            }
            break;

          case "result":
            sessionId = message.session_id;
            if (message.subtype === "success" && "result" in message) {
              emit({ type: "text", content: message.result });
            }
            if (message.subtype !== "success" && "errors" in message) {
              for (const err of message.errors) {
                emit({ type: "error", content: err });
              }
            }
            break;
        }
      }

      meta.sessionId = sessionId;
      meta.lastActiveAt = Date.now();
      setThread(meta);
      emit({ type: "done" });
    } catch (err) {
      const errMsg = err instanceof Error ? err.message : String(err);
      console.error(`[worker:${threadId}] error: ${errMsg}`);
      emit({ type: "error", content: errMsg });
    }
  }
}
