import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { ConversationLoop } from "./loop.js";
import { SessionStore } from "./session.js";
import { ToolRegistry } from "@forge/tools";
import type {
  LLMProvider,
  ChatRequest,
  ChatDelta,
  OutboundEvent,
  ContextBundle,
} from "@forge/types";

// Mock provider: returns simple text (no tool use)
class MockTextProvider implements LLMProvider {
  async *chat(_request: ChatRequest): AsyncIterable<ChatDelta> {
    yield { type: "text_delta", text: "E Pluribus Anus" };
    yield { type: "message_stop", stopReason: "end_turn" };
  }
}

// Mock provider: first call uses a tool, second call returns text
class MockToolProvider implements LLMProvider {
  private callCount = 0;
  async *chat(_request: ChatRequest): AsyncIterable<ChatDelta> {
    if (this.callCount === 0) {
      this.callCount++;
      yield { type: "tool_use_start", id: "tu_1", name: "TestEcho" };
      yield { type: "tool_use_delta", id: "0", partialJson: '{"msg":"six seasons"}' };
      yield { type: "tool_use_end", id: "0" };
      yield { type: "message_stop", stopReason: "tool_use" };
    } else {
      yield { type: "text_delta", text: "and a movie" };
      yield { type: "message_stop", stopReason: "end_turn" };
    }
  }
}

const emptyBundle: ContextBundle = {
  claudeMd: [],
  rules: [],
  skillDescriptions: [],
  agentDefinitions: {},
  settings: {},
};

describe("ConversationLoop", () => {
  let dir: string;

  beforeEach(() => {
    dir = join(tmpdir(), `forge-loop-${randomUUID()}`);
    mkdirSync(dir, { recursive: true });
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("streams text response and emits done", async () => {
    const loop = new ConversationLoop({
      provider: new MockTextProvider(),
      tools: new ToolRegistry(),
      context: emptyBundle,
      cwd: dir,
      sessionStore: new SessionStore(join(dir, "sessions")),
      threadId: "t1",
    });

    const events: OutboundEvent[] = [];
    for await (const event of loop.send("Hello Dean Pelton")) {
      events.push(event);
    }

    const textEvents = events.filter((e) => e.type === "text");
    assert.ok(textEvents.length > 0);
    assert.ok(textEvents.some((e) => e.content?.includes("E Pluribus Anus")));

    const doneEvents = events.filter((e) => e.type === "done");
    assert.equal(doneEvents.length, 1);
  });

  it("executes tool calls and continues", async () => {
    const registry = new ToolRegistry();
    registry.register({
      name: "TestEcho",
      description: "Echo a message",
      inputSchema: { type: "object", properties: { msg: { type: "string" } } },
      async handler(input) {
        return { content: [{ type: "text", text: `echo: ${input.msg}` }] };
      },
    });

    const loop = new ConversationLoop({
      provider: new MockToolProvider(),
      tools: registry,
      context: emptyBundle,
      cwd: dir,
      sessionStore: new SessionStore(join(dir, "sessions")),
      threadId: "t2",
    });

    const events: OutboundEvent[] = [];
    for await (const event of loop.send("Do the thing")) {
      events.push(event);
    }

    const toolEvents = events.filter((e) => e.type === "tool_use");
    assert.ok(toolEvents.length > 0);
    assert.equal(toolEvents[0].toolName, "TestEcho");

    const textEvents = events.filter((e) => e.type === "text");
    assert.ok(textEvents.some((e) => e.content?.includes("and a movie")));
  });

  it("has a sessionId after send", async () => {
    const loop = new ConversationLoop({
      provider: new MockTextProvider(),
      tools: new ToolRegistry(),
      context: emptyBundle,
      cwd: dir,
      sessionStore: new SessionStore(join(dir, "sessions")),
      threadId: "t3",
    });

    for await (const _ of loop.send("test")) {
      // drain
    }

    assert.ok(loop.sessionId);
    assert.equal(typeof loop.sessionId, "string");
  });

  it("respects maxTurns", async () => {
    // Provider that always returns tool use (infinite loop)
    class InfiniteToolProvider implements LLMProvider {
      async *chat(_request: ChatRequest): AsyncIterable<ChatDelta> {
        yield { type: "tool_use_start", id: randomUUID(), name: "TestEcho" };
        yield { type: "tool_use_delta", id: "0", partialJson: '{"msg":"loop"}' };
        yield { type: "tool_use_end", id: "0" };
        yield { type: "message_stop", stopReason: "tool_use" };
      }
    }

    const registry = new ToolRegistry();
    registry.register({
      name: "TestEcho",
      description: "Echo",
      inputSchema: { type: "object", properties: { msg: { type: "string" } } },
      async handler() {
        return { content: [{ type: "text", text: "echoed" }] };
      },
    });

    const loop = new ConversationLoop({
      provider: new InfiniteToolProvider(),
      tools: registry,
      context: emptyBundle,
      cwd: dir,
      sessionStore: new SessionStore(join(dir, "sessions")),
      threadId: "t4",
      maxTurns: 3,
    });

    const events: OutboundEvent[] = [];
    for await (const event of loop.send("infinite loop")) {
      events.push(event);
    }

    const errorEvents = events.filter((e) => e.type === "error");
    assert.ok(errorEvents.some((e) => e.content?.includes("Max turns")));
    assert.ok(events.some((e) => e.type === "done"));
  });
});
