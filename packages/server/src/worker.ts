import { randomUUID } from "node:crypto";
import {
  ConversationLoop,
  ContextLoader,
  AnthropicProvider,
  SessionStore,
} from "@forge/runtime";
import { createDefaultRegistry } from "@forge/tools";
import { getThread, setThread, pullMessage, publishEvent } from "./bus.js";
import { config } from "./config.js";
import type { OutboundEvent, ThreadMeta } from "@forge/types";

export async function startWorker(threadId: string): Promise<void> {
  const cwd = config.worker.workspaceDir;
  const provider = new AnthropicProvider({
    apiKey: process.env.ANTHROPIC_API_KEY!,
  });
  const tools = createDefaultRegistry(cwd);
  const contextLoader = new ContextLoader(cwd);
  const context = await contextLoader.load(["user", "project", "local"]);
  const sessionStore = new SessionStore(config.worker.sessionsDir);

  const meta: ThreadMeta = getThread(threadId) ?? {
    threadId,
    metadata: {},
    createdAt: Date.now(),
    lastActiveAt: Date.now(),
  };
  let sessionId = meta.sessionId;

  const emit = (partial: Omit<Partial<OutboundEvent>, "id" | "threadId" | "timestamp">): void => {
    publishEvent(threadId, {
      id: randomUUID(),
      threadId,
      type: "text",
      timestamp: Date.now(),
      ...partial,
    });
  };

  console.log(`[worker:${threadId}] started, session=${sessionId ?? "new"}`);

  while (true) {
    const msg = await pullMessage(threadId);
    console.log(`[worker:${threadId}] <- ${msg.text.slice(0, 100)}`);

    try {
      const loop = new ConversationLoop({
        provider,
        tools,
        context,
        cwd,
        sessionStore,
        threadId,
        model: context.settings.model ?? "claude-sonnet-4-5-20250929",
      });

      const generator = sessionId
        ? loop.resume(sessionId, msg.text)
        : loop.send(msg.text);

      for await (const event of generator) {
        publishEvent(threadId, event);
      }

      sessionId = loop.sessionId;
      meta.sessionId = sessionId;
      meta.lastActiveAt = Date.now();
      setThread(meta);
    } catch (err) {
      const errMsg = err instanceof Error ? err.message : String(err);
      console.error(`[worker:${threadId}] error: ${errMsg}`);
      emit({ type: "error", content: errMsg });
    }
  }
}
