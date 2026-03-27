import { randomUUID } from "node:crypto";
import type {
  LLMProvider,
  ChatRequest,
  ChatMessage,
  ChatContentBlock,
  ToolResultContent,
  OutboundEvent,
  ContextBundle,
  ThinkingConfig,
  ToolContext,
} from "@forge/types";
import type { ToolRegistry } from "@forge/tools";
import { assembleSystemPrompt } from "./prompt.js";
import type { SessionStore } from "./session.js";

interface ConversationLoopOptions {
  provider: LLMProvider;
  tools: ToolRegistry;
  context: ContextBundle;
  cwd: string;
  sessionStore: SessionStore;
  threadId: string;
  model?: string;
  thinking?: ThinkingConfig;
  maxTurns?: number;
  signal?: AbortSignal;
}

export class ConversationLoop {
  private _sessionId: string;
  private history: ChatMessage[] = [];
  private provider: LLMProvider;
  private tools: ToolRegistry;
  private context: ContextBundle;
  private cwd: string;
  private sessionStore: SessionStore;
  private threadId: string;
  private model: string;
  private maxTurns: number;
  private signal: AbortSignal;

  constructor(opts: ConversationLoopOptions) {
    this._sessionId = randomUUID();
    this.provider = opts.provider;
    this.tools = opts.tools;
    this.context = opts.context;
    this.cwd = opts.cwd;
    this.sessionStore = opts.sessionStore;
    this.threadId = opts.threadId;
    this.model = opts.model ?? "claude-sonnet-4-5-20250929";
    this.maxTurns = opts.maxTurns ?? 100;
    this.signal = opts.signal ?? new AbortController().signal;
  }

  get sessionId(): string {
    return this._sessionId;
  }

  async *send(prompt: string): AsyncIterable<OutboundEvent> {
    this.history.push({
      role: "user",
      content: [{ type: "text", text: prompt }],
    });

    await this.sessionStore.append(this._sessionId, {
      uuid: randomUUID(),
      sessionId: this._sessionId,
      type: "user",
      message: { role: "user", content: [{ type: "text", text: prompt }] },
      timestamp: Date.now(),
    });

    yield* this.runLoop();
  }

  async *resume(sessionId: string, prompt: string): AsyncIterable<OutboundEvent> {
    this._sessionId = sessionId;
    const messages = await this.sessionStore.load(sessionId);
    for (const msg of messages) {
      const m = msg.message as ChatMessage;
      if (m.role === "user") {
        // Could be a text message or a tool_result message — preserve as-is
        this.history.push({
          role: "user",
          content: m.content as ChatContentBlock[],
        });
      } else if (m.role === "assistant") {
        this.history.push({
          role: "assistant",
          content: m.content as ChatContentBlock[],
        });
      }
    }
    yield* this.send(prompt);
  }

  private async *runLoop(): AsyncIterable<OutboundEvent> {
    let turns = 0;

    while (turns < this.maxTurns) {
      if (this.signal.aborted) break;
      turns++;

      const systemBlocks = assembleSystemPrompt(this.context, this.tools, this.cwd);
      const request: ChatRequest = {
        model: this.model,
        system: systemBlocks,
        messages: this.history,
        tools: this.tools.schemas(),
        maxTokens: 16384,
        stream: true,
      };

      // Collect assistant message from stream
      const assistantBlocks: ChatContentBlock[] = [];
      let currentToolUse: { id: string; name: string; jsonParts: string[] } | null = null;

      for await (const delta of this.provider.chat(request)) {
        switch (delta.type) {
          case "text_delta":
            yield this.makeEvent("text", delta.text);
            if (
              assistantBlocks.length === 0 ||
              assistantBlocks[assistantBlocks.length - 1].type !== "text"
            ) {
              assistantBlocks.push({ type: "text", text: delta.text });
            } else {
              const last = assistantBlocks[assistantBlocks.length - 1];
              if (last.type === "text") last.text += delta.text;
            }
            break;

          case "tool_use_start":
            currentToolUse = { id: delta.id, name: delta.name, jsonParts: [] };
            yield this.makeEvent("tool_use", undefined, delta.name);
            break;

          case "tool_use_delta":
            if (currentToolUse) {
              currentToolUse.jsonParts.push(delta.partialJson);
            }
            break;

          case "tool_use_end":
            if (currentToolUse) {
              let parsedInput: Record<string, unknown> = {};
              const fullJson = currentToolUse.jsonParts.join("");
              if (fullJson) {
                try {
                  parsedInput = JSON.parse(fullJson) as Record<string, unknown>;
                } catch {
                  /* empty input is fine */
                }
              }
              assistantBlocks.push({
                type: "tool_use",
                id: currentToolUse.id,
                name: currentToolUse.name,
                input: parsedInput,
              });
              currentToolUse = null;
            }
            break;

          case "message_stop":
            break;
        }
      }

      // Add assistant message to history + persist
      this.history.push({ role: "assistant", content: assistantBlocks });
      await this.sessionStore.append(this._sessionId, {
        uuid: randomUUID(),
        sessionId: this._sessionId,
        type: "assistant",
        message: { role: "assistant", content: assistantBlocks },
        timestamp: Date.now(),
      });

      // Check for tool_use blocks
      const toolUseBlocks = assistantBlocks.filter((b) => b.type === "tool_use");

      if (toolUseBlocks.length === 0) {
        yield this.makeEvent("done");
        return;
      }

      // Execute tools
      const toolResults: ChatContentBlock[] = [];
      for (const block of toolUseBlocks) {
        if (block.type !== "tool_use") continue;

        const ctx: ToolContext = {
          cwd: this.cwd,
          sessionId: this._sessionId,
          threadId: this.threadId,
          signal: this.signal,
          emit: () => {},
        };

        const result = await this.tools.execute(block.name, block.input, ctx);
        toolResults.push({
          type: "tool_result",
          tool_use_id: block.id,
          content: result.content as ToolResultContent[],
        });
      }

      // Add tool results as user message (Anthropic API convention)
      const toolResultMessage: ChatMessage = { role: "user", content: toolResults };
      this.history.push(toolResultMessage);
      await this.sessionStore.append(this._sessionId, {
        uuid: randomUUID(),
        sessionId: this._sessionId,
        type: "user",
        message: toolResultMessage,
        timestamp: Date.now(),
      });
    }

    yield this.makeEvent("error", "Max turns reached");
    yield this.makeEvent("done");
  }

  private makeEvent(
    type: OutboundEvent["type"],
    content?: string,
    toolName?: string,
  ): OutboundEvent {
    return {
      id: randomUUID(),
      threadId: this.threadId,
      type,
      content,
      toolName,
      timestamp: Date.now(),
    };
  }
}
