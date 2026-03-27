import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { AnthropicProvider } from "./anthropic.js";

describe("AnthropicProvider", () => {
  it("constructs with api key", () => {
    const provider = new AnthropicProvider({ apiKey: "test-key" });
    assert.ok(provider);
  });

  it("chat is a function", () => {
    const provider = new AnthropicProvider({ apiKey: "test-key" });
    assert.equal(typeof provider.chat, "function");
  });
});
