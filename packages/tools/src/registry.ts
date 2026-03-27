import type {
  ToolDefinition,
  ToolSchema,
  ToolResult,
  ToolContext,
} from "@forge/types";

export class ToolRegistry {
  private tools = new Map<string, ToolDefinition>();

  register(tool: ToolDefinition): void {
    this.tools.set(tool.name, tool);
  }

  get(name: string): ToolDefinition | undefined {
    return this.tools.get(name);
  }

  all(): ToolDefinition[] {
    return [...this.tools.values()];
  }

  schemas(): ToolSchema[] {
    return this.all().map((t) => ({
      name: t.name,
      description: t.description,
      input_schema: t.inputSchema,
    }));
  }

  async execute(
    name: string,
    input: Record<string, unknown>,
    ctx: ToolContext,
  ): Promise<ToolResult> {
    const tool = this.tools.get(name);
    if (!tool) {
      return {
        content: [{ type: "text", text: `Unknown tool: ${name}` }],
        isError: true,
      };
    }
    return tool.handler(input, ctx);
  }
}

export function createDefaultRegistry(_cwd: string): ToolRegistry {
  const registry = new ToolRegistry();
  // Tools registered in subsequent tasks
  return registry;
}
