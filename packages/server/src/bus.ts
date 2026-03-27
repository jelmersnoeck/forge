import { EventEmitter } from "node:events";
import type { InboundMessage, OutboundEvent, ThreadMeta } from "@forge/types";

// ── Thread metadata (replaces Redis GET/SET on thread:<id>) ──

const threads = new Map<string, ThreadMeta>();

export function getThread(threadId: string): ThreadMeta | undefined {
  return threads.get(threadId);
}

export function setThread(meta: ThreadMeta): void {
  threads.set(meta.threadId, meta);
}

// ── Message queue (replaces Redis LPUSH/BRPOP on inbox:<id>) ──
//
//   pushMessage  → gateway enqueues work
//   pullMessage  → worker blocks until a message arrives
//
// Uses a waiter list: if a worker is already waiting when a message
// arrives, we resolve its promise directly — no polling, no timers.

const queues = new Map<string, InboundMessage[]>();
const waiters = new Map<string, Array<(msg: InboundMessage) => void>>();

export function pushMessage(threadId: string, msg: InboundMessage): void {
  const pending = waiters.get(threadId);
  if (pending?.length) {
    pending.shift()!(msg);
    return;
  }
  let queue = queues.get(threadId);
  if (!queue) {
    queue = [];
    queues.set(threadId, queue);
  }
  queue.push(msg);
}

export function pullMessage(threadId: string): Promise<InboundMessage> {
  const queue = queues.get(threadId);
  if (queue?.length) {
    return Promise.resolve(queue.shift()!);
  }
  return new Promise((resolve) => {
    let list = waiters.get(threadId);
    if (!list) {
      list = [];
      waiters.set(threadId, list);
    }
    list.push(resolve);
  });
}

// ── Event bus (replaces Redis PUBLISH/SUBSCRIBE on output:<id>) ──

const emitter = new EventEmitter();
emitter.setMaxListeners(0);

export function publishEvent(threadId: string, event: OutboundEvent): void {
  emitter.emit(`output:${threadId}`, event);
}

export function subscribeEvents(
  threadId: string,
  handler: (event: OutboundEvent) => void,
): () => void {
  const channel = `output:${threadId}`;
  emitter.on(channel, handler);
  return () => {
    emitter.off(channel, handler);
  };
}
