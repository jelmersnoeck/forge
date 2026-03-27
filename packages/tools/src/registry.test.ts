import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { ToolRegistry } from "./registry.js";
import type { ToolDefinition, ToolResult } from "@forge/types";

const fakeTool: ToolDefinition = {
  name: "TestTool",
  description: "A test tool for Greendale Community College",
  inputSchema: {
    type: "object",
    properties: { name: { type: "string" } },
    required: ["name"],
  },
  handler: async (input): Promise<ToolResult> => ({
    content: [{ type: "text", text: `Hello, ${input.name}` }],
  }),
};

describe("ToolRegistry", () => {
  it("registers and retrieves a tool by name", () => {
    const registry = new ToolRegistry();
    registry.register(fakeTool);
    assert.equal(registry.get("TestTool")?.name, "TestTool");
  });

  it("returns undefined for unknown tool", () => {
    const registry = new ToolRegistry();
    assert.equal(registry.get("Nonexistent"), undefined);
  });

  it("lists all registered tools", () => {
    const registry = new ToolRegistry();
    registry.register(fakeTool);
    assert.equal(registry.all().length, 1);
    assert.equal(registry.all()[0].name, "TestTool");
  });

  it("produces schemas array for API requests", () => {
    const registry = new ToolRegistry();
    registry.register(fakeTool);
    const schemas = registry.schemas();
    assert.equal(schemas.length, 1);
    assert.equal(schemas[0].name, "TestTool");
    assert.equal(schemas[0].description, "A test tool for Greendale Community College");
    assert.deepEqual(schemas[0].input_schema, fakeTool.inputSchema);
  });

  it("executes a tool and returns result", async () => {
    const registry = new ToolRegistry();
    registry.register(fakeTool);
    const ctx = {
      cwd: "/tmp",
      sessionId: "s1",
      threadId: "t1",
      signal: new AbortController().signal,
      emit: () => {},
    };
    const result = await registry.execute("TestTool", { name: "Troy Barnes" }, ctx);
    assert.equal(result.content[0].type, "text");
    assert.equal((result.content[0] as { text: string }).text, "Hello, Troy Barnes");
  });

  it("returns error result for unknown tool", async () => {
    const registry = new ToolRegistry();
    const ctx = {
      cwd: "/tmp",
      sessionId: "s1",
      threadId: "t1",
      signal: new AbortController().signal,
      emit: () => {},
    };
    const result = await registry.execute("Nope", {}, ctx);
    assert.equal(result.isError, true);
  });
});
