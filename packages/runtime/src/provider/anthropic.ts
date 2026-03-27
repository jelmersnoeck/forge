import Anthropic from "@anthropic-ai/sdk";
import type {
  LLMProvider,
  ChatRequest,
  ChatDelta,
  ChatStream,
} from "@forge/types";

export class AnthropicProvider implements LLMProvider {
  private client: Anthropic;

  constructor(opts: { apiKey: string; baseUrl?: string }) {
    this.client = new Anthropic({
      apiKey: opts.apiKey,
      ...(opts.baseUrl ? { baseURL: opts.baseUrl } : {}),
    });
  }

  async *chat(request: ChatRequest): ChatStream {
    const stream = this.client.messages.stream({
      model: request.model,
      max_tokens: request.maxTokens,
      system: request.system.map((b) => ({
        type: "text" as const,
        text: b.text,
        ...(b.cacheControl ? { cache_control: b.cacheControl } : {}),
      })),
      messages: request.messages.map((m) => ({
        role: m.role,
        content: m.content.map((block) => {
          switch (block.type) {
            case "text":
              return { type: "text" as const, text: block.text };
            case "tool_use":
              return {
                type: "tool_use" as const,
                id: block.id,
                name: block.name,
                input: block.input,
              };
            case "tool_result":
              return {
                type: "tool_result" as const,
                tool_use_id: block.tool_use_id,
                content: block.content.map((c) => {
                  if (c.type === "text") {
                    return { type: "text" as const, text: c.text };
                  }
                  return {
                    type: "image" as const,
                    source: {
                      type: "base64" as const,
                      media_type: c.source.media_type as "image/jpeg" | "image/png" | "image/gif" | "image/webp",
                      data: c.source.data,
                    },
                  };
                }),
              };
            default:
              return block;
          }
        }),
      })),
      tools: request.tools.map((t) => ({
        name: t.name,
        description: t.description,
        input_schema: t.input_schema as Anthropic.Tool.InputSchema,
      })),
      stream: true,
    });

    for await (const event of stream) {
      const delta = this.mapEvent(event);
      if (delta) yield delta;
    }
  }

  private mapEvent(event: Anthropic.MessageStreamEvent): ChatDelta | null {
    switch (event.type) {
      case "content_block_start": {
        const block = event.content_block;
        if (block.type === "tool_use") {
          return { type: "tool_use_start", id: block.id, name: block.name };
        }
        return null;
      }
      case "content_block_delta": {
        const delta = event.delta;
        if (delta.type === "text_delta") {
          return { type: "text_delta", text: delta.text };
        }
        if (delta.type === "input_json_delta") {
          return {
            type: "tool_use_delta",
            id: String(event.index),
            partialJson: delta.partial_json,
          };
        }
        return null;
      }
      case "content_block_stop":
        return { type: "tool_use_end", id: String(event.index) };
      case "message_delta":
        if (event.delta.stop_reason) {
          return { type: "message_stop", stopReason: event.delta.stop_reason };
        }
        return null;
      default:
        return null;
    }
  }
}
