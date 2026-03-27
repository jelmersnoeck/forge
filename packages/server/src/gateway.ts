import Fastify from "fastify";
import { randomUUID } from "node:crypto";
import { getThread, setThread, pushMessage, subscribeEvents } from "./bus.js";
import { config } from "./config.js";
import { startWorker } from "./worker.js";
import type { InboundMessage, ThreadMeta } from "@forge/types";

const activeWorkers = new Map<string, Promise<void>>();

function ensureWorker(threadId: string): void {
  if (activeWorkers.has(threadId)) return;

  const task = startWorker(threadId).catch((err) => {
    console.error(`[gateway] worker ${threadId} died:`, err);
    activeWorkers.delete(threadId);
  });
  activeWorkers.set(threadId, task);
}

export async function startGateway(): Promise<void> {
  const app = Fastify({ logger: true });

  // POST /threads — create a new conversation thread.
  //
  // Body (optional):
  //   { metadata?: Record<string, unknown> }
  //
  // Adapters use metadata to store platform-specific correlation data:
  //   Slack:  { channel: "C123", thread_ts: "1711..." }
  //   Linear: { issueId: "LIN-456" }
  //   DD:     { incidentId: "inc-789" }
  app.post("/threads", async (req, reply) => {
    const body = req.body as { metadata?: Record<string, unknown> } | undefined;
    const threadId = randomUUID();
    const now = Date.now();

    const meta: ThreadMeta = {
      threadId,
      metadata: body?.metadata ?? {},
      createdAt: now,
      lastActiveAt: now,
    };
    setThread(meta);

    ensureWorker(threadId);
    return reply.status(201).send({ threadId, metadata: meta.metadata });
  });

  // GET /threads/:threadId — retrieve thread metadata.
  app.get<{
    Params: { threadId: string };
  }>("/threads/:threadId", async (req, reply) => {
    const meta = getThread(req.params.threadId);
    if (!meta) {
      return reply.status(404).send({ error: "thread not found" });
    }
    return reply.send(meta);
  });

  // POST /threads/:threadId/messages — send a message into a thread.
  //
  // Body:
  //   { text: string, user?: string, source?: string, metadata?: Record<string, unknown> }
  app.post<{
    Params: { threadId: string };
  }>("/threads/:threadId/messages", async (req, reply) => {
    const { threadId } = req.params;
    const body = req.body as {
      text?: string;
      user?: string;
      source?: string;
      metadata?: Record<string, unknown>;
    } | undefined;

    if (!body?.text) {
      return reply.status(400).send({ error: "text is required" });
    }

    ensureWorker(threadId);

    const msg: InboundMessage = {
      threadId,
      text: body.text,
      user: body.user ?? "anonymous",
      source: body.source ?? "api",
      timestamp: Date.now(),
      ...(body.metadata ? { metadata: body.metadata } : {}),
    };

    pushMessage(threadId, msg);
    return reply.status(202).send({ status: "queued" });
  });

  // GET /threads/:threadId/events — SSE stream of agent output.
  app.get<{
    Params: { threadId: string };
  }>("/threads/:threadId/events", async (req, reply) => {
    const { threadId } = req.params;

    reply.raw.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
    });

    const unsubscribe = subscribeEvents(threadId, (event) => {
      reply.raw.write(`id: ${event.id}\ndata: ${JSON.stringify(event)}\n\n`);
    });

    req.raw.on("close", unsubscribe);
  });

  await app.listen({ port: config.gateway.port, host: config.gateway.host });
  console.log(`[gateway] listening on ${config.gateway.host}:${config.gateway.port}`);
}
