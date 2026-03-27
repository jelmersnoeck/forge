import type { OutboundEvent } from "@forge/types";

export class ConversationLoop {
  constructor(_opts: Record<string, unknown>) {}
  get sessionId(): string { return "stub"; }
  async *send(_prompt: string): AsyncIterable<OutboundEvent> {}
  async *resume(_sessionId: string, _prompt: string): AsyncIterable<OutboundEvent> {}
}
