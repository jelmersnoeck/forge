# SDK Decoupling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `@anthropic-ai/claude-agent-sdk` with forge-owned packages (`@forge/types`, `@forge/tools`, `@forge/runtime`) that drive the Anthropic Messages API directly while loading CLAUDE.md, skills, agents, and rules from the filesystem.

**Architecture:** Three new packages layered bottom-up: types (contracts) -> tools (registry + in-process handlers) -> runtime (conversation loop, context loading, provider abstraction). The server's worker.ts is the only file that changes its import. Gateway, bus, and CLI remain untouched.

**Tech Stack:** TypeScript ESM, Anthropic SDK (`@anthropic-ai/sdk`), `fast-glob`, `node-html-markdown`, ripgrep (external binary), Node `child_process`/`fs`

**Spec:** `docs/superpowers/specs/2026-03-27-sdk-decoupling-design.md`

---

## File Structure

### New files

```
packages/types/src/
  index.ts                    (modify — add new type exports)

packages/tools/
  package.json                (create)
  tsconfig.json               (create)
  src/
    index.ts                  (create — re-exports)
    registry.ts               (create — ToolRegistry class)
    registry.test.ts          (create — registry tests)
    tools/
      read.ts                 (create — Read tool)
      read.test.ts            (create)
      write.ts                (create — Write tool)
      write.test.ts           (create)
      edit.ts                 (create — Edit tool)
      edit.test.ts            (create)
      bash.ts                 (create — Bash tool)
      bash.test.ts            (create)
      glob.ts                 (create — Glob tool)
      glob.test.ts            (create)
      grep.ts                 (create — Grep tool)
      grep.test.ts            (create)

packages/runtime/
  package.json                (create)
  tsconfig.json               (create)
  src/
    index.ts                  (create — re-exports)
    provider/
      interface.ts            (create — LLMProvider re-export)
      anthropic.ts            (create — AnthropicProvider)
      anthropic.test.ts       (create)
    context.ts                (create — ContextLoader)
    context.test.ts           (create)
    prompt.ts                 (create — system prompt assembly)
    prompt.test.ts            (create)
    session.ts                (create — SessionStore)
    session.test.ts           (create)
    loop.ts                   (create — ConversationLoop)
    loop.test.ts              (create)
```

### Modified files

```
packages/types/src/index.ts       (add new types)
packages/server/src/config.ts     (add sessionsDir)
packages/server/src/worker.ts     (replace SDK with runtime)
packages/server/package.json      (swap deps)
package.json                      (add workspaces)
tsconfig.json                     (add project references)
justfile                          (add new build targets)
```

---

### Task 1: Expand `@forge/types` with new contracts

**Files:**
- Modify: `packages/types/src/index.ts`

- [ ] **Step 1: Write type definitions for provider abstraction**

Add to `packages/types/src/index.ts` after the existing types:

```typescript
// ── LLM Provider ─────────────────────────────────────────────

export interface SystemBlock {
  type: "text";
  text: string;
  cacheControl?: { type: "ephemeral" };
}

export type ThinkingConfig =
  | { type: "adaptive" }
  | { type: "enabled"; budgetTokens: number }
  | { type: "disabled" };

export interface ChatRequest {
  model: string;
  system: SystemBlock[];
  messages: ChatMessage[];
  tools: ToolSchema[];
  maxTokens: number;
  thinking?: ThinkingConfig;
  stream: true;
}

export type ChatMessage =
  | { role: "user"; content: ChatContentBlock[] }
  | { role: "assistant"; content: ChatContentBlock[] };

export type ChatContentBlock =
  | { type: "text"; text: string }
  | { type: "tool_use"; id: string; name: string; input: Record<string, unknown> }
  | { type: "tool_result"; tool_use_id: string; content: ToolResultContent[] };

export type ChatDelta =
  | { type: "text_delta"; text: string }
  | { type: "thinking_delta"; thinking: string }
  | { type: "tool_use_start"; id: string; name: string }
  | { type: "tool_use_delta"; id: string; partialJson: string }
  | { type: "tool_use_end"; id: string }
  | { type: "message_stop"; stopReason: string };

export type ChatStream = AsyncIterable<ChatDelta>;

export interface LLMProvider {
  chat(request: ChatRequest): ChatStream;
}
```

- [ ] **Step 2: Write type definitions for tool system**

Continue appending to `packages/types/src/index.ts`:

```typescript
// ── Tool System ──────────────────────────────────────────────

export interface ToolSchema {
  name: string;
  description: string;
  input_schema: Record<string, unknown>;
}

export interface ToolResult {
  content: ToolResultContent[];
  isError?: boolean;
}

export type ToolResultContent =
  | { type: "text"; text: string }
  | { type: "image"; source: { type: "base64"; media_type: string; data: string } };

export interface ToolContext {
  cwd: string;
  sessionId: string;
  threadId: string;
  signal: AbortSignal;
  emit: (event: OutboundEvent) => void;
  runtime?: RuntimeHandle;
}

export interface ToolDefinition {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  handler: (input: Record<string, unknown>, ctx: ToolContext) => Promise<ToolResult>;
  annotations?: { readOnly?: boolean; destructive?: boolean };
}
```

- [ ] **Step 3: Write type definitions for context loading**

Continue appending to `packages/types/src/index.ts`:

```typescript
// ── Context Loading ──────────────────────────────────────────

export interface ContextBundle {
  claudeMd: ClaudeMdEntry[];
  rules: RuleEntry[];
  skillDescriptions: SkillDescription[];
  agentDefinitions: Record<string, AgentDefinition>;
  settings: MergedSettings;
}

export interface ClaudeMdEntry {
  path: string;
  content: string;
  level: "user" | "project" | "local" | "parent";
}

export interface RuleEntry {
  path: string;
  content: string;
  level: "user" | "project";
}

export interface SkillDescription {
  name: string;
  description: string;
  path: string;
  isUserInvocable: boolean;
}

export interface AgentDefinition {
  name: string;
  description: string;
  prompt: string;
  tools?: string[];
  disallowedTools?: string[];
  model?: "sonnet" | "opus" | "haiku" | "inherit";
  maxTurns?: number;
}

export interface MergedSettings {
  permissions?: { allow: string[]; deny: string[] };
  env?: Record<string, string>;
  model?: string;
}
```

- [ ] **Step 4: Write type definitions for session persistence and runtime handle**

Continue appending to `packages/types/src/index.ts`:

```typescript
// ── Session Persistence ──────────────────────────────────────

export interface SessionMessage {
  uuid: string;
  parentUuid?: string;
  sessionId: string;
  type: "user" | "assistant" | "system";
  message: unknown;
  timestamp: number;
}

export interface SessionMeta {
  sessionId: string;
  threadId: string;
  cwd: string;
  createdAt: number;
  lastActiveAt: number;
  title?: string;
}

// ── Runtime Handle (for Skill/Agent tools) ───────────────────

export interface RuntimeHandle {
  loadSkillContent(name: string): Promise<string>;
  spawnSubagent(opts: SubagentOptions): Promise<ToolResult>;
}

export interface SubagentOptions {
  prompt: string;
  agentType: string;
  model?: string;
  tools?: string[];
  maxTurns?: number;
}
```

- [ ] **Step 5: Build types and verify**

Run: `just build-types`
Expected: clean compile, no errors

- [ ] **Step 6: Commit**

```bash
git add packages/types/src/index.ts
git commit -m "feat(types): add provider, tool, context, and session type contracts"
```

---

### Task 2: Scaffold `@forge/tools` package + ToolRegistry

**Files:**
- Create: `packages/tools/package.json`
- Create: `packages/tools/tsconfig.json`
- Create: `packages/tools/src/index.ts`
- Create: `packages/tools/src/registry.ts`
- Create: `packages/tools/src/registry.test.ts`
- Modify: `package.json` (root — add workspace)
- Modify: `tsconfig.json` (root — add reference)
- Modify: `justfile` (add build-tools)

- [ ] **Step 1: Write the failing test for ToolRegistry**

Create `packages/tools/src/registry.test.ts`:

```typescript
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
```

- [ ] **Step 2: Create package scaffold files**

Create `packages/tools/package.json`:

```json
{
  "name": "@forge/tools",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "exports": {
    ".": {
      "types": "./dist/index.d.ts",
      "import": "./dist/index.js"
    }
  },
  "scripts": {
    "build": "tsc",
    "test": "node --test dist/**/*.test.js"
  },
  "dependencies": {
    "@forge/types": "*",
    "fast-glob": "^3.3.0"
  },
  "devDependencies": {
    "@types/node": "^22.0.0",
    "typescript": "^5.7.0"
  }
}
```

Create `packages/tools/tsconfig.json`:

```json
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": {
    "outDir": "dist",
    "rootDir": "src"
  },
  "include": ["src/**/*"],
  "references": [
    { "path": "../types" }
  ]
}
```

Create `packages/tools/src/index.ts`:

```typescript
export { ToolRegistry, createDefaultRegistry } from "./registry.js";
```

- [ ] **Step 3: Update root workspace config**

Add `"packages/tools"` to root `package.json` workspaces array (insert before `"packages/server"`).

Add `{ "path": "packages/tools" }` to root `tsconfig.json` references (insert before server).

- [ ] **Step 4: Run the test to verify it fails**

Run: `npm install && just build-types && cd packages/tools && npx tsc && node --test dist/**/*.test.js`
Expected: FAIL — `registry.js` does not exist yet

- [ ] **Step 5: Implement ToolRegistry**

Create `packages/tools/src/registry.ts`:

```typescript
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
```

- [ ] **Step 6: Build and run tests**

Run: `cd packages/tools && npx tsc && node --test dist/**/*.test.js`
Expected: all 5 tests PASS

- [ ] **Step 7: Update justfile**

Add build-tools recipe to `justfile` after `build-types`:

```just
# Build tools
build-tools: build-types
  npm run build -w @forge/tools
```

Update `build` recipe:

```just
build: build-types build-tools build-server build-cli
```

- [ ] **Step 8: Commit**

```bash
git add packages/tools/ package.json tsconfig.json justfile
git commit -m "feat(tools): scaffold @forge/tools package with ToolRegistry"
```

---

### Task 3: Implement Read tool

**Files:**
- Create: `packages/tools/src/tools/read.ts`
- Create: `packages/tools/src/tools/read.test.ts`
- Modify: `packages/tools/src/registry.ts` (register in createDefaultRegistry)

- [ ] **Step 1: Write the failing test**

Create `packages/tools/src/tools/read.test.ts`:

```typescript
import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { readTool } from "./read.js";
import type { ToolContext } from "@forge/types";

const makeCtx = (cwd: string): ToolContext => ({
  cwd,
  sessionId: "s1",
  threadId: "t1",
  signal: new AbortController().signal,
  emit: () => {},
});

describe("Read tool", () => {
  let dir: string;

  beforeEach(() => {
    dir = join(tmpdir(), `forge-test-${randomUUID()}`);
    mkdirSync(dir, { recursive: true });
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("reads a text file with line numbers", async () => {
    const file = join(dir, "test.txt");
    writeFileSync(file, "line one\nline two\nline three\n");
    const result = await readTool.handler({ file_path: file }, makeCtx(dir));
    assert.equal(result.isError, undefined);
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes("1\t"), "should have line numbers");
    assert.ok(text.includes("line one"));
    assert.ok(text.includes("line three"));
  });

  it("supports offset and limit", async () => {
    const file = join(dir, "big.txt");
    writeFileSync(file, "a\nb\nc\nd\ne\n");
    const result = await readTool.handler(
      { file_path: file, offset: 2, limit: 2 },
      makeCtx(dir),
    );
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes("b"), "should start at line 2");
    assert.ok(text.includes("c"), "should include line 3");
    assert.ok(!text.includes("d"), "should not include line 4");
  });

  it("returns error for nonexistent file", async () => {
    const result = await readTool.handler(
      { file_path: join(dir, "nope.txt") },
      makeCtx(dir),
    );
    assert.equal(result.isError, true);
  });

  it("reads an image file as base64", async () => {
    const file = join(dir, "img.png");
    // minimal PNG: 1x1 pixel
    const png = Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
      "base64",
    );
    writeFileSync(file, png);
    const result = await readTool.handler({ file_path: file }, makeCtx(dir));
    assert.equal(result.content[0].type, "image");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/tools && npx tsc && node --test dist/tools/read.test.js`
Expected: FAIL — `read.js` does not exist

- [ ] **Step 3: Implement Read tool**

Create `packages/tools/src/tools/read.ts`:

```typescript
import { readFile, stat } from "node:fs/promises";
import type { ToolDefinition, ToolResult } from "@forge/types";

const IMAGE_EXTENSIONS = new Set([".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg"]);

function isImagePath(path: string): boolean {
  const ext = path.slice(path.lastIndexOf(".")).toLowerCase();
  return IMAGE_EXTENSIONS.has(ext);
}

export const readTool: ToolDefinition = {
  name: "Read",
  description:
    "Reads a file from the local filesystem. The file_path must be an absolute path. " +
    "By default reads up to 2000 lines. Use offset and limit for large files. " +
    "Can read images (PNG, JPG, etc.) — returns them as base64.",
  inputSchema: {
    type: "object",
    properties: {
      file_path: { type: "string", description: "Absolute path to the file" },
      offset: { type: "number", description: "Line number to start reading from (1-based)" },
      limit: { type: "number", description: "Number of lines to read" },
    },
    required: ["file_path"],
  },
  annotations: { readOnly: true },
  async handler(input): Promise<ToolResult> {
    const filePath = input.file_path as string;
    const offset = (input.offset as number | undefined) ?? 1;
    const limit = (input.limit as number | undefined) ?? 2000;

    try {
      await stat(filePath);
    } catch {
      return {
        content: [{ type: "text", text: `File not found: ${filePath}` }],
        isError: true,
      };
    }

    if (isImagePath(filePath)) {
      const buf = await readFile(filePath);
      const ext = filePath.slice(filePath.lastIndexOf(".") + 1).toLowerCase();
      const mimeMap: Record<string, string> = {
        png: "image/png",
        jpg: "image/jpeg",
        jpeg: "image/jpeg",
        gif: "image/gif",
        webp: "image/webp",
        svg: "image/svg+xml",
      };
      return {
        content: [
          {
            type: "image",
            source: {
              type: "base64",
              media_type: mimeMap[ext] ?? "application/octet-stream",
              data: buf.toString("base64"),
            },
          },
        ],
      };
    }

    const raw = await readFile(filePath, "utf-8");
    const allLines = raw.split("\n");
    // offset is 1-based in Claude Code convention
    const startIdx = Math.max(0, offset - 1);
    const sliced = allLines.slice(startIdx, startIdx + limit);
    const numbered = sliced
      .map((line, i) => `${startIdx + i + 1}\t${line}`)
      .join("\n");

    return {
      content: [{ type: "text", text: numbered }],
    };
  },
};
```

- [ ] **Step 4: Register in createDefaultRegistry**

In `packages/tools/src/registry.ts`, add import and registration:

```typescript
import { readTool } from "./tools/read.js";
```

In `createDefaultRegistry`, add:

```typescript
registry.register(readTool);
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd packages/tools && npx tsc && node --test dist/tools/read.test.js`
Expected: all 4 tests PASS

- [ ] **Step 6: Commit**

```bash
git add packages/tools/src/tools/read.ts packages/tools/src/tools/read.test.ts packages/tools/src/registry.ts
git commit -m "feat(tools): implement Read tool"
```

---

### Task 4: Implement Write tool

**Files:**
- Create: `packages/tools/src/tools/write.ts`
- Create: `packages/tools/src/tools/write.test.ts`
- Modify: `packages/tools/src/registry.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/tools/src/tools/write.test.ts`:

```typescript
import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, rmSync, existsSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { writeTool } from "./write.js";
import type { ToolContext } from "@forge/types";

const makeCtx = (cwd: string): ToolContext => ({
  cwd,
  sessionId: "s1",
  threadId: "t1",
  signal: new AbortController().signal,
  emit: () => {},
});

describe("Write tool", () => {
  let dir: string;

  beforeEach(() => {
    dir = join(tmpdir(), `forge-test-${randomUUID()}`);
    mkdirSync(dir, { recursive: true });
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("creates a new file", async () => {
    const file = join(dir, "new.txt");
    await writeTool.handler({ file_path: file, content: "streets ahead" }, makeCtx(dir));
    assert.equal(readFileSync(file, "utf-8"), "streets ahead");
  });

  it("creates parent directories", async () => {
    const file = join(dir, "deep", "nested", "file.txt");
    await writeTool.handler({ file_path: file, content: "pop pop" }, makeCtx(dir));
    assert.ok(existsSync(file));
    assert.equal(readFileSync(file, "utf-8"), "pop pop");
  });

  it("overwrites existing file", async () => {
    const file = join(dir, "existing.txt");
    await writeTool.handler({ file_path: file, content: "old" }, makeCtx(dir));
    await writeTool.handler({ file_path: file, content: "new" }, makeCtx(dir));
    assert.equal(readFileSync(file, "utf-8"), "new");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/tools && npx tsc && node --test dist/tools/write.test.js`
Expected: FAIL

- [ ] **Step 3: Implement Write tool**

Create `packages/tools/src/tools/write.ts`:

```typescript
import { writeFile, mkdir } from "node:fs/promises";
import { dirname } from "node:path";
import type { ToolDefinition, ToolResult } from "@forge/types";

export const writeTool: ToolDefinition = {
  name: "Write",
  description:
    "Writes a file to the local filesystem. Creates parent directories if needed. " +
    "Overwrites existing files. The file_path must be an absolute path.",
  inputSchema: {
    type: "object",
    properties: {
      file_path: { type: "string", description: "Absolute path to the file to write" },
      content: { type: "string", description: "Content to write to the file" },
    },
    required: ["file_path", "content"],
  },
  annotations: { destructive: true },
  async handler(input): Promise<ToolResult> {
    const filePath = input.file_path as string;
    const content = input.content as string;

    try {
      await mkdir(dirname(filePath), { recursive: true });
      await writeFile(filePath, content, "utf-8");
      return {
        content: [{ type: "text", text: `Wrote ${content.length} bytes to ${filePath}` }],
      };
    } catch (err) {
      return {
        content: [{ type: "text", text: `Failed to write ${filePath}: ${err}` }],
        isError: true,
      };
    }
  },
};
```

- [ ] **Step 4: Register in createDefaultRegistry, build, test**

Add `import { writeTool } from "./tools/write.js";` and `registry.register(writeTool);` in `registry.ts`.

Run: `cd packages/tools && npx tsc && node --test dist/tools/write.test.js`
Expected: all 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add packages/tools/src/tools/write.ts packages/tools/src/tools/write.test.ts packages/tools/src/registry.ts
git commit -m "feat(tools): implement Write tool"
```

---

### Task 5: Implement Edit tool

**Files:**
- Create: `packages/tools/src/tools/edit.ts`
- Create: `packages/tools/src/tools/edit.test.ts`
- Modify: `packages/tools/src/registry.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/tools/src/tools/edit.test.ts`:

```typescript
import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { editTool } from "./edit.js";
import type { ToolContext } from "@forge/types";

const makeCtx = (cwd: string): ToolContext => ({
  cwd,
  sessionId: "s1",
  threadId: "t1",
  signal: new AbortController().signal,
  emit: () => {},
});

describe("Edit tool", () => {
  let dir: string;

  beforeEach(() => {
    dir = join(tmpdir(), `forge-test-${randomUUID()}`);
    mkdirSync(dir, { recursive: true });
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("replaces a unique string", async () => {
    const file = join(dir, "test.ts");
    writeFileSync(file, 'const name = "Abed";\n');
    const result = await editTool.handler(
      { file_path: file, old_string: '"Abed"', new_string: '"Troy"' },
      makeCtx(dir),
    );
    assert.equal(result.isError, undefined);
    assert.equal(readFileSync(file, "utf-8"), 'const name = "Troy";\n');
  });

  it("fails if old_string is not unique and replace_all is false", async () => {
    const file = join(dir, "test.ts");
    writeFileSync(file, "foo bar foo baz\n");
    const result = await editTool.handler(
      { file_path: file, old_string: "foo", new_string: "qux" },
      makeCtx(dir),
    );
    assert.equal(result.isError, true);
  });

  it("replaces all occurrences when replace_all is true", async () => {
    const file = join(dir, "test.ts");
    writeFileSync(file, "foo bar foo baz\n");
    const result = await editTool.handler(
      { file_path: file, old_string: "foo", new_string: "qux", replace_all: true },
      makeCtx(dir),
    );
    assert.equal(result.isError, undefined);
    assert.equal(readFileSync(file, "utf-8"), "qux bar qux baz\n");
  });

  it("fails if old_string not found at all", async () => {
    const file = join(dir, "test.ts");
    writeFileSync(file, "hello world\n");
    const result = await editTool.handler(
      { file_path: file, old_string: "missing", new_string: "nope" },
      makeCtx(dir),
    );
    assert.equal(result.isError, true);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/tools && npx tsc && node --test dist/tools/edit.test.js`
Expected: FAIL

- [ ] **Step 3: Implement Edit tool**

Create `packages/tools/src/tools/edit.ts`:

```typescript
import { readFile, writeFile } from "node:fs/promises";
import type { ToolDefinition, ToolResult } from "@forge/types";

export const editTool: ToolDefinition = {
  name: "Edit",
  description:
    "Performs exact string replacements in files. The old_string must be unique " +
    "in the file unless replace_all is true.",
  inputSchema: {
    type: "object",
    properties: {
      file_path: { type: "string", description: "Absolute path to the file to edit" },
      old_string: { type: "string", description: "The text to replace" },
      new_string: { type: "string", description: "The replacement text" },
      replace_all: { type: "boolean", description: "Replace all occurrences (default false)" },
    },
    required: ["file_path", "old_string", "new_string"],
  },
  annotations: { destructive: true },
  async handler(input): Promise<ToolResult> {
    const filePath = input.file_path as string;
    const oldStr = input.old_string as string;
    const newStr = input.new_string as string;
    const replaceAll = (input.replace_all as boolean) ?? false;

    let content: string;
    try {
      content = await readFile(filePath, "utf-8");
    } catch {
      return {
        content: [{ type: "text", text: `File not found: ${filePath}` }],
        isError: true,
      };
    }

    const count = content.split(oldStr).length - 1;

    if (count === 0) {
      return {
        content: [{ type: "text", text: `old_string not found in ${filePath}` }],
        isError: true,
      };
    }

    if (count > 1 && !replaceAll) {
      return {
        content: [
          {
            type: "text",
            text: `old_string appears ${count} times in ${filePath}. Use replace_all or provide more context.`,
          },
        ],
        isError: true,
      };
    }

    const updated = replaceAll
      ? content.replaceAll(oldStr, newStr)
      : content.replace(oldStr, newStr);

    await writeFile(filePath, updated, "utf-8");
    return {
      content: [{ type: "text", text: `Edited ${filePath}` }],
    };
  },
};
```

- [ ] **Step 4: Register, build, test**

Add import and registration in `registry.ts`.

Run: `cd packages/tools && npx tsc && node --test dist/tools/edit.test.js`
Expected: all 4 tests PASS

- [ ] **Step 5: Commit**

```bash
git add packages/tools/src/tools/edit.ts packages/tools/src/tools/edit.test.ts packages/tools/src/registry.ts
git commit -m "feat(tools): implement Edit tool"
```

---

### Task 6: Implement Bash tool

**Files:**
- Create: `packages/tools/src/tools/bash.ts`
- Create: `packages/tools/src/tools/bash.test.ts`
- Modify: `packages/tools/src/registry.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/tools/src/tools/bash.test.ts`:

```typescript
import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { bashTool } from "./bash.js";
import type { ToolContext } from "@forge/types";

const makeCtx = (cwd: string): ToolContext => ({
  cwd,
  sessionId: "s1",
  threadId: "t1",
  signal: new AbortController().signal,
  emit: () => {},
});

describe("Bash tool", () => {
  let dir: string;

  beforeEach(() => {
    dir = join(tmpdir(), `forge-test-${randomUUID()}`);
    mkdirSync(dir, { recursive: true });
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("runs a command and captures stdout", async () => {
    const result = await bashTool.handler({ command: "echo 'cool cool cool'" }, makeCtx(dir));
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes("cool cool cool"));
  });

  it("captures stderr on failure", async () => {
    const result = await bashTool.handler({ command: "ls /nonexistent_abed_path" }, makeCtx(dir));
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.length > 0, "should have error output");
  });

  it("respects cwd", async () => {
    const result = await bashTool.handler({ command: "pwd" }, makeCtx(dir));
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes(dir));
  });

  it("times out long commands", async () => {
    const result = await bashTool.handler(
      { command: "sleep 30", timeout: 1000 },
      makeCtx(dir),
    );
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes("timed out") || text.includes("killed"), `got: ${text}`);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/tools && npx tsc && node --test dist/tools/bash.test.js`
Expected: FAIL

- [ ] **Step 3: Implement Bash tool**

Create `packages/tools/src/tools/bash.ts`:

```typescript
import { spawn } from "node:child_process";
import type { ToolDefinition, ToolResult, ToolContext } from "@forge/types";

export const bashTool: ToolDefinition = {
  name: "Bash",
  description:
    "Executes a bash command. Working directory is set from context. " +
    "Timeout defaults to 120000ms (2 minutes).",
  inputSchema: {
    type: "object",
    properties: {
      command: { type: "string", description: "The bash command to execute" },
      timeout: { type: "number", description: "Timeout in milliseconds (max 600000)" },
      description: { type: "string", description: "Description of what this command does" },
    },
    required: ["command"],
  },
  annotations: { destructive: true },
  async handler(input, ctx: ToolContext): Promise<ToolResult> {
    const command = input.command as string;
    const timeout = Math.min((input.timeout as number | undefined) ?? 120_000, 600_000);

    return new Promise((resolve) => {
      const chunks: Buffer[] = [];
      const errChunks: Buffer[] = [];
      let timedOut = false;

      const proc = spawn("bash", ["-c", command], {
        cwd: ctx.cwd,
        stdio: ["ignore", "pipe", "pipe"],
        env: { ...process.env },
      });

      proc.stdout.on("data", (d: Buffer) => chunks.push(d));
      proc.stderr.on("data", (d: Buffer) => errChunks.push(d));

      const timer = setTimeout(() => {
        timedOut = true;
        proc.kill("SIGTERM");
        setTimeout(() => {
          if (!proc.killed) proc.kill("SIGKILL");
        }, 5000);
      }, timeout);

      proc.on("close", (code) => {
        clearTimeout(timer);
        const stdout = Buffer.concat(chunks).toString();
        const stderr = Buffer.concat(errChunks).toString();

        if (timedOut) {
          resolve({
            content: [
              {
                type: "text",
                text: `Command timed out after ${timeout}ms\nstdout: ${stdout}\nstderr: ${stderr}`,
              },
            ],
            isError: true,
          });
          return;
        }

        const output = [stdout, stderr].filter(Boolean).join("\n");
        resolve({
          content: [{ type: "text", text: output || "(no output)" }],
          isError: code !== 0,
        });
      });

      proc.on("error", (err) => {
        clearTimeout(timer);
        resolve({
          content: [{ type: "text", text: `Failed to spawn: ${err.message}` }],
          isError: true,
        });
      });
    });
  },
};
```

- [ ] **Step 4: Register, build, test**

Add import and registration in `registry.ts`.

Run: `cd packages/tools && npx tsc && node --test dist/tools/bash.test.js`
Expected: all 4 tests PASS

- [ ] **Step 5: Commit**

```bash
git add packages/tools/src/tools/bash.ts packages/tools/src/tools/bash.test.ts packages/tools/src/registry.ts
git commit -m "feat(tools): implement Bash tool"
```

---

### Task 7: Implement Glob tool

**Files:**
- Create: `packages/tools/src/tools/glob.ts`
- Create: `packages/tools/src/tools/glob.test.ts`
- Modify: `packages/tools/src/registry.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/tools/src/tools/glob.test.ts`:

```typescript
import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { globTool } from "./glob.js";
import type { ToolContext } from "@forge/types";

const makeCtx = (cwd: string): ToolContext => ({
  cwd,
  sessionId: "s1",
  threadId: "t1",
  signal: new AbortController().signal,
  emit: () => {},
});

describe("Glob tool", () => {
  let dir: string;

  beforeEach(() => {
    dir = join(tmpdir(), `forge-test-${randomUUID()}`);
    mkdirSync(join(dir, "src"), { recursive: true });
    writeFileSync(join(dir, "src", "app.ts"), "");
    writeFileSync(join(dir, "src", "util.ts"), "");
    writeFileSync(join(dir, "readme.md"), "");
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("finds files matching a pattern", async () => {
    const result = await globTool.handler({ pattern: "**/*.ts" }, makeCtx(dir));
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes("app.ts"));
    assert.ok(text.includes("util.ts"));
    assert.ok(!text.includes("readme.md"));
  });

  it("uses custom path", async () => {
    const result = await globTool.handler(
      { pattern: "*.ts", path: join(dir, "src") },
      makeCtx(dir),
    );
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes("app.ts"));
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/tools && npx tsc && node --test dist/tools/glob.test.js`
Expected: FAIL

- [ ] **Step 3: Implement Glob tool**

Create `packages/tools/src/tools/glob.ts`:

```typescript
import fg from "fast-glob";
import type { ToolDefinition, ToolResult, ToolContext } from "@forge/types";

export const globTool: ToolDefinition = {
  name: "Glob",
  description: "Fast file pattern matching. Returns matching file paths.",
  inputSchema: {
    type: "object",
    properties: {
      pattern: { type: "string", description: "Glob pattern (e.g. **/*.ts)" },
      path: { type: "string", description: "Directory to search in (defaults to cwd)" },
    },
    required: ["pattern"],
  },
  annotations: { readOnly: true },
  async handler(input, ctx: ToolContext): Promise<ToolResult> {
    const pattern = input.pattern as string;
    const searchPath = (input.path as string | undefined) ?? ctx.cwd;

    try {
      const files = await fg(pattern, {
        cwd: searchPath,
        dot: true,
        onlyFiles: true,
        stats: true,
      });
      const sorted = files.sort(
        (a, b) => (b.stats?.mtimeMs ?? 0) - (a.stats?.mtimeMs ?? 0),
      );
      const paths = sorted.map((f) => f.path);
      return {
        content: [{ type: "text", text: paths.join("\n") || "(no matches)" }],
      };
    } catch (err) {
      return {
        content: [{ type: "text", text: `Glob error: ${err}` }],
        isError: true,
      };
    }
  },
};
```

- [ ] **Step 4: Register, build, test**

Add import and registration in `registry.ts`.

Run: `cd packages/tools && npx tsc && node --test dist/tools/glob.test.js`
Expected: all 2 tests PASS

- [ ] **Step 5: Commit**

```bash
git add packages/tools/src/tools/glob.ts packages/tools/src/tools/glob.test.ts packages/tools/src/registry.ts
git commit -m "feat(tools): implement Glob tool"
```

---

### Task 8: Implement Grep tool

**Files:**
- Create: `packages/tools/src/tools/grep.ts`
- Create: `packages/tools/src/tools/grep.test.ts`
- Modify: `packages/tools/src/registry.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/tools/src/tools/grep.test.ts`:

```typescript
import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { grepTool } from "./grep.js";
import type { ToolContext } from "@forge/types";

const makeCtx = (cwd: string): ToolContext => ({
  cwd,
  sessionId: "s1",
  threadId: "t1",
  signal: new AbortController().signal,
  emit: () => {},
});

describe("Grep tool", () => {
  let dir: string;

  beforeEach(() => {
    dir = join(tmpdir(), `forge-test-${randomUUID()}`);
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, "a.ts"), "const dean = 'Craig Pelton';\n");
    writeFileSync(join(dir, "b.ts"), "const student = 'Jeff Winger';\n");
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("finds files matching a pattern", async () => {
    const result = await grepTool.handler({ pattern: "Pelton" }, makeCtx(dir));
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes("a.ts"));
  });

  it("returns content when output_mode is content", async () => {
    const result = await grepTool.handler(
      { pattern: "Winger", output_mode: "content" },
      makeCtx(dir),
    );
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes("Jeff Winger"));
  });

  it("returns no matches gracefully", async () => {
    const result = await grepTool.handler({ pattern: "Magnitude" }, makeCtx(dir));
    const text = (result.content[0] as { text: string }).text;
    assert.ok(text.includes("no match") || text === "(no matches)");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/tools && npx tsc && node --test dist/tools/grep.test.js`
Expected: FAIL

- [ ] **Step 3: Implement Grep tool**

Create `packages/tools/src/tools/grep.ts`:

```typescript
import { spawn } from "node:child_process";
import type { ToolDefinition, ToolResult, ToolContext } from "@forge/types";

export const grepTool: ToolDefinition = {
  name: "Grep",
  description:
    "Search tool built on ripgrep. Supports regex, file type filtering, and output modes.",
  inputSchema: {
    type: "object",
    properties: {
      pattern: { type: "string", description: "Regex pattern to search for" },
      path: { type: "string", description: "File or directory to search in" },
      glob: { type: "string", description: "Glob pattern to filter files" },
      output_mode: {
        type: "string",
        enum: ["content", "files_with_matches", "count"],
        description: "Output mode (default: files_with_matches)",
      },
      "-i": { type: "boolean", description: "Case insensitive search" },
      "-n": { type: "boolean", description: "Show line numbers" },
      "-C": { type: "number", description: "Context lines around matches" },
      head_limit: { type: "number", description: "Limit output to first N entries" },
    },
    required: ["pattern"],
  },
  annotations: { readOnly: true },
  async handler(input, ctx: ToolContext): Promise<ToolResult> {
    const pattern = input.pattern as string;
    const searchPath = (input.path as string | undefined) ?? ctx.cwd;
    const mode = (input.output_mode as string | undefined) ?? "files_with_matches";
    const caseInsensitive = input["-i"] as boolean | undefined;
    const lineNumbers = input["-n"] as boolean | undefined;
    const context = input["-C"] as number | undefined;
    const globFilter = input.glob as string | undefined;
    const headLimit = input.head_limit as number | undefined;

    const args: string[] = [];

    switch (mode) {
      case "files_with_matches":
        args.push("--files-with-matches");
        break;
      case "count":
        args.push("--count");
        break;
      default:
        // content mode: no extra flag
        break;
    }

    if (caseInsensitive) args.push("-i");
    if (lineNumbers !== false && mode === "content") args.push("-n");
    if (context !== undefined) args.push("-C", String(context));
    if (globFilter) args.push("--glob", globFilter);

    args.push("--", pattern, searchPath);

    return new Promise((resolve) => {
      const chunks: Buffer[] = [];
      const proc = spawn("rg", args, { cwd: ctx.cwd, stdio: ["ignore", "pipe", "pipe"] });

      proc.stdout.on("data", (d: Buffer) => chunks.push(d));
      proc.stderr.on("data", (d: Buffer) => chunks.push(d));

      proc.on("close", () => {
        let output = Buffer.concat(chunks).toString().trim();
        if (!output) output = "(no matches)";

        if (headLimit && headLimit > 0) {
          const lines = output.split("\n");
          output = lines.slice(0, headLimit).join("\n");
        }

        resolve({ content: [{ type: "text", text: output }] });
      });

      proc.on("error", (err) => {
        resolve({
          content: [{ type: "text", text: `Grep error (is rg installed?): ${err.message}` }],
          isError: true,
        });
      });
    });
  },
};
```

- [ ] **Step 4: Register, build, test**

Add import and registration in `registry.ts`.

Run: `cd packages/tools && npx tsc && node --test dist/tools/grep.test.js`
Expected: all 3 tests PASS (requires `rg` on PATH)

- [ ] **Step 5: Commit**

```bash
git add packages/tools/src/tools/grep.ts packages/tools/src/tools/grep.test.ts packages/tools/src/registry.ts
git commit -m "feat(tools): implement Grep tool (ripgrep)"
```

---

### Task 9: Scaffold `@forge/runtime` + AnthropicProvider

**Files:**
- Create: `packages/runtime/package.json`
- Create: `packages/runtime/tsconfig.json`
- Create: `packages/runtime/src/index.ts`
- Create: `packages/runtime/src/provider/anthropic.ts`
- Create: `packages/runtime/src/provider/anthropic.test.ts`
- Modify: `package.json` (root — add workspace)
- Modify: `tsconfig.json` (root — add reference)
- Modify: `justfile` (add build-runtime)

- [ ] **Step 1: Create package scaffold**

Create `packages/runtime/package.json`:

```json
{
  "name": "@forge/runtime",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "exports": {
    ".": {
      "types": "./dist/index.d.ts",
      "import": "./dist/index.js"
    }
  },
  "scripts": {
    "build": "tsc",
    "test": "node --test dist/**/*.test.js"
  },
  "dependencies": {
    "@anthropic-ai/sdk": "^0.39.0",
    "@forge/types": "*",
    "@forge/tools": "*"
  },
  "devDependencies": {
    "@types/node": "^22.0.0",
    "typescript": "^5.7.0"
  }
}
```

Create `packages/runtime/tsconfig.json`:

```json
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": {
    "outDir": "dist",
    "rootDir": "src"
  },
  "include": ["src/**/*"],
  "references": [
    { "path": "../types" },
    { "path": "../tools" }
  ]
}
```

Create `packages/runtime/src/index.ts`:

```typescript
export { AnthropicProvider } from "./provider/anthropic.js";
export { ContextLoader } from "./context.js";
export { assembleSystemPrompt } from "./prompt.js";
export { SessionStore } from "./session.js";
export { ConversationLoop } from "./loop.js";
```

- [ ] **Step 2: Update root workspace config**

Add `"packages/runtime"` to root `package.json` workspaces (before `"packages/server"`).

Add `{ "path": "packages/runtime" }` to root `tsconfig.json` references.

Update `justfile` — add `build-runtime` recipe:

```just
# Build runtime
build-runtime: build-types build-tools
  npm run build -w @forge/runtime
```

Update `build` recipe:

```just
build: build-types build-tools build-runtime build-server build-cli
```

Update root `package.json` build script similarly.

- [ ] **Step 3: Write the AnthropicProvider test**

Create `packages/runtime/src/provider/anthropic.test.ts`:

```typescript
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { AnthropicProvider } from "./anthropic.js";

describe("AnthropicProvider", () => {
  it("constructs with api key", () => {
    const provider = new AnthropicProvider({ apiKey: "test-key" });
    assert.ok(provider);
  });

  it("chat returns an async iterable", async () => {
    // We can't call the real API in unit tests, but we can verify
    // the method exists and the provider is structurally correct
    const provider = new AnthropicProvider({ apiKey: "test-key" });
    assert.equal(typeof provider.chat, "function");
  });
});
```

- [ ] **Step 4: Implement AnthropicProvider**

Create `packages/runtime/src/provider/anthropic.ts`:

```typescript
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
                  if (c.type === "text") return { type: "text" as const, text: c.text };
                  return c;
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
        if ("thinking" in delta) {
          return {
            type: "thinking_delta",
            thinking: (delta as { thinking: string }).thinking,
          };
        }
        return null;
      }
      case "content_block_stop": {
        // We track tool_use_end by watching content_block_stop after a tool_use_start
        return { type: "tool_use_end", id: String(event.index) };
      }
      case "message_stop":
        return { type: "message_stop", stopReason: "end_turn" };
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
```

- [ ] **Step 5: Run `npm install`, build, test**

Run: `npm install && cd packages/runtime && npx tsc && node --test dist/provider/anthropic.test.js`
Expected: 2 tests PASS

- [ ] **Step 6: Commit**

```bash
git add packages/runtime/ package.json tsconfig.json justfile
git commit -m "feat(runtime): scaffold @forge/runtime with AnthropicProvider"
```

---

### Task 10: Implement ContextLoader

**Files:**
- Create: `packages/runtime/src/context.ts`
- Create: `packages/runtime/src/context.test.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/runtime/src/context.test.ts`:

```typescript
import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { ContextLoader } from "./context.js";

describe("ContextLoader", () => {
  let dir: string;

  beforeEach(() => {
    dir = join(tmpdir(), `forge-ctx-${randomUUID()}`);
    mkdirSync(dir, { recursive: true });
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("loads CLAUDE.md from project root", async () => {
    writeFileSync(join(dir, "CLAUDE.md"), "# Greendale rules");
    const loader = new ContextLoader(dir);
    const bundle = await loader.load(["project"]);
    assert.equal(bundle.claudeMd.length, 1);
    assert.ok(bundle.claudeMd[0].content.includes("Greendale rules"));
    assert.equal(bundle.claudeMd[0].level, "project");
  });

  it("loads CLAUDE.md from .claude/ directory", async () => {
    mkdirSync(join(dir, ".claude"), { recursive: true });
    writeFileSync(join(dir, ".claude", "CLAUDE.md"), "# Study room F");
    const loader = new ContextLoader(dir);
    const bundle = await loader.load(["project"]);
    assert.equal(bundle.claudeMd.length, 1);
    assert.ok(bundle.claudeMd[0].content.includes("Study room F"));
  });

  it("loads rules from .claude/rules/", async () => {
    mkdirSync(join(dir, ".claude", "rules"), { recursive: true });
    writeFileSync(join(dir, ".claude", "rules", "no-cheating.md"), "No cheating on exams");
    const loader = new ContextLoader(dir);
    const bundle = await loader.load(["project"]);
    assert.equal(bundle.rules.length, 1);
    assert.ok(bundle.rules[0].content.includes("No cheating"));
  });

  it("discovers skills with descriptions", async () => {
    const skillDir = join(dir, ".claude", "skills", "paintball");
    mkdirSync(skillDir, { recursive: true });
    writeFileSync(
      join(skillDir, "SKILL.md"),
      "---\nname: paintball\ndescription: Strategic paintball planning\n---\nFull content here",
    );
    const loader = new ContextLoader(dir);
    const bundle = await loader.load(["project"]);
    assert.equal(bundle.skillDescriptions.length, 1);
    assert.equal(bundle.skillDescriptions[0].name, "paintball");
    assert.equal(bundle.skillDescriptions[0].description, "Strategic paintball planning");
  });

  it("loads skill content lazily", async () => {
    const skillDir = join(dir, ".claude", "skills", "pillow-fort");
    mkdirSync(skillDir, { recursive: true });
    writeFileSync(
      join(skillDir, "SKILL.md"),
      "---\nname: pillow-fort\ndescription: Build epic pillow forts\n---\nDetailed instructions here",
    );
    const loader = new ContextLoader(dir);
    await loader.load(["project"]);
    const content = await loader.loadSkillContent("pillow-fort");
    assert.ok(content.includes("Detailed instructions here"));
  });

  it("loads CLAUDE.local.md when local source included", async () => {
    writeFileSync(join(dir, "CLAUDE.local.md"), "# My local secrets");
    const loader = new ContextLoader(dir);
    const bundle = await loader.load(["local"]);
    assert.equal(bundle.claudeMd.length, 1);
    assert.equal(bundle.claudeMd[0].level, "local");
  });

  it("discovers agent definitions from .claude/agents/", async () => {
    mkdirSync(join(dir, ".claude", "agents"), { recursive: true });
    writeFileSync(
      join(dir, ".claude", "agents", "inspector.md"),
      '---\nname: inspector\ndescription: "Inspects code quality"\nmodel: haiku\n---\nYou are a code inspector.',
    );
    const loader = new ContextLoader(dir);
    const bundle = await loader.load(["project"]);
    assert.ok(bundle.agentDefinitions["inspector"]);
    assert.equal(bundle.agentDefinitions["inspector"].description, "Inspects code quality");
    assert.equal(bundle.agentDefinitions["inspector"].model, "haiku");
    assert.ok(bundle.agentDefinitions["inspector"].prompt.includes("code inspector"));
  });

  it("merges settings from .claude/settings.json", async () => {
    mkdirSync(join(dir, ".claude"), { recursive: true });
    writeFileSync(
      join(dir, ".claude", "settings.json"),
      JSON.stringify({ model: "claude-opus-4-6", permissions: { allow: ["Read"], deny: [] } }),
    );
    const loader = new ContextLoader(dir);
    const bundle = await loader.load(["project"]);
    assert.equal(bundle.settings.model, "claude-opus-4-6");
    assert.deepEqual(bundle.settings.permissions?.allow, ["Read"]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/runtime && npx tsc && node --test dist/context.test.js`
Expected: FAIL — `context.js` does not exist

- [ ] **Step 3: Implement ContextLoader**

Create `packages/runtime/src/context.ts`:

```typescript
import { readFile, readdir, stat } from "node:fs/promises";
import { join, dirname } from "node:path";
import { homedir } from "node:os";
import type {
  ContextBundle,
  ClaudeMdEntry,
  RuleEntry,
  SkillDescription,
  AgentDefinition,
  MergedSettings,
} from "@forge/types";

function parseFrontmatter(raw: string): { meta: Record<string, unknown>; body: string } {
  const match = raw.match(/^---\n([\s\S]*?)\n---\n?([\s\S]*)$/);
  if (!match) return { meta: {}, body: raw };

  const meta: Record<string, unknown> = {};
  for (const line of match[1].split("\n")) {
    const idx = line.indexOf(":");
    if (idx < 0) continue;
    const key = line.slice(0, idx).trim();
    let val: string | unknown = line.slice(idx + 1).trim();
    // Strip surrounding quotes
    if (typeof val === "string" && val.startsWith('"') && val.endsWith('"')) {
      val = val.slice(1, -1);
    }
    meta[key] = val;
  }
  return { meta, body: match[2].trim() };
}

async function tryReadFile(path: string): Promise<string | null> {
  try {
    return await readFile(path, "utf-8");
  } catch {
    return null;
  }
}

async function tryReadDir(path: string): Promise<string[]> {
  try {
    return await readdir(path);
  } catch {
    return [];
  }
}

async function isDir(path: string): Promise<boolean> {
  try {
    return (await stat(path)).isDirectory();
  } catch {
    return false;
  }
}

export class ContextLoader {
  private skillPaths = new Map<string, string>();

  constructor(private cwd: string) {}

  async load(sources: ("user" | "project" | "local")[]): Promise<ContextBundle> {
    const claudeMd: ClaudeMdEntry[] = [];
    const rules: RuleEntry[] = [];
    const skillDescriptions: SkillDescription[] = [];
    const agentDefinitions: Record<string, AgentDefinition> = {};
    let settings: MergedSettings = {};

    if (sources.includes("user")) {
      const userDir = join(homedir(), ".claude");
      await this.loadClaudeMd(userDir, "user", claudeMd);
      await this.loadRules(join(userDir, "rules"), "user", rules);
      await this.loadSkills(join(userDir, "skills"), skillDescriptions);
      settings = await this.mergeSettings(settings, join(userDir, "settings.json"));
    }

    if (sources.includes("project")) {
      // Walk parent directories for CLAUDE.md
      await this.loadParentClaudeMd(this.cwd, claudeMd);

      // Project root
      await this.loadClaudeMd(this.cwd, "project", claudeMd);
      const dotClaude = join(this.cwd, ".claude");
      await this.loadClaudeMd(dotClaude, "project", claudeMd);
      await this.loadRules(join(dotClaude, "rules"), "project", rules);
      await this.loadSkills(join(dotClaude, "skills"), skillDescriptions);
      await this.loadAgents(join(dotClaude, "agents"), agentDefinitions);
      settings = await this.mergeSettings(settings, join(dotClaude, "settings.json"));
    }

    if (sources.includes("local")) {
      const localMd = await tryReadFile(join(this.cwd, "CLAUDE.local.md"));
      if (localMd) {
        claudeMd.push({
          path: join(this.cwd, "CLAUDE.local.md"),
          content: localMd,
          level: "local",
        });
      }
      settings = await this.mergeSettings(
        settings,
        join(this.cwd, ".claude", "settings.local.json"),
      );
    }

    return { claudeMd, rules, skillDescriptions, agentDefinitions, settings };
  }

  async loadSkillContent(name: string): Promise<string> {
    const path = this.skillPaths.get(name);
    if (!path) throw new Error(`Skill not found: ${name}`);
    const raw = await readFile(path, "utf-8");
    return raw;
  }

  private async loadClaudeMd(
    dir: string,
    level: ClaudeMdEntry["level"],
    entries: ClaudeMdEntry[],
  ): Promise<void> {
    const content = await tryReadFile(join(dir, "CLAUDE.md"));
    if (content) {
      entries.push({ path: join(dir, "CLAUDE.md"), content, level });
    }
  }

  private async loadParentClaudeMd(
    startDir: string,
    entries: ClaudeMdEntry[],
  ): Promise<void> {
    let current = dirname(startDir);
    while (current !== startDir) {
      const content = await tryReadFile(join(current, "CLAUDE.md"));
      if (content) {
        entries.push({ path: join(current, "CLAUDE.md"), content, level: "parent" });
      }
      const next = dirname(current);
      if (next === current) break;
      startDir = current;
      current = next;
    }
  }

  private async loadRules(
    dir: string,
    level: RuleEntry["level"],
    entries: RuleEntry[],
  ): Promise<void> {
    const files = await tryReadDir(dir);
    for (const file of files) {
      if (!file.endsWith(".md")) continue;
      const content = await tryReadFile(join(dir, file));
      if (content) {
        entries.push({ path: join(dir, file), content, level });
      }
    }
  }

  private async loadSkills(
    dir: string,
    descriptions: SkillDescription[],
  ): Promise<void> {
    const entries = await tryReadDir(dir);
    for (const entry of entries) {
      const skillDir = join(dir, entry);
      if (!(await isDir(skillDir))) continue;
      const skillFile = join(skillDir, "SKILL.md");
      const raw = await tryReadFile(skillFile);
      if (!raw) continue;

      const { meta } = parseFrontmatter(raw);
      const name = (meta.name as string) ?? entry;
      const description = (meta.description as string) ?? "";
      const isUserInvocable = (meta.userInvocable as boolean) ?? false;

      this.skillPaths.set(name, skillFile);
      descriptions.push({ name, description, path: skillFile, isUserInvocable });
    }
  }

  private async loadAgents(
    dir: string,
    definitions: Record<string, AgentDefinition>,
  ): Promise<void> {
    const files = await tryReadDir(dir);
    for (const file of files) {
      if (!file.endsWith(".md")) continue;
      const raw = await tryReadFile(join(dir, file));
      if (!raw) continue;

      const { meta, body } = parseFrontmatter(raw);
      const name = (meta.name as string) ?? file.replace(/\.md$/, "");
      definitions[name] = {
        name,
        description: (meta.description as string) ?? "",
        prompt: body,
        tools: meta.tools as string[] | undefined,
        disallowedTools: meta.disallowedTools as string[] | undefined,
        model: meta.model as AgentDefinition["model"],
        maxTurns: meta.maxTurns as number | undefined,
      };
    }
  }

  private async mergeSettings(
    existing: MergedSettings,
    path: string,
  ): Promise<MergedSettings> {
    const raw = await tryReadFile(path);
    if (!raw) return existing;

    try {
      const parsed = JSON.parse(raw) as MergedSettings;
      return {
        ...existing,
        ...parsed,
        permissions: parsed.permissions ?? existing.permissions,
        env: { ...existing.env, ...parsed.env },
      };
    } catch {
      return existing;
    }
  }
}
```

- [ ] **Step 4: Build and run tests**

Run: `cd packages/runtime && npx tsc && node --test dist/context.test.js`
Expected: all 8 tests PASS

- [ ] **Step 5: Commit**

```bash
git add packages/runtime/src/context.ts packages/runtime/src/context.test.ts
git commit -m "feat(runtime): implement ContextLoader for CLAUDE.md, skills, agents, rules"
```

---

### Task 11: Implement system prompt assembly

**Files:**
- Create: `packages/runtime/src/prompt.ts`
- Create: `packages/runtime/src/prompt.test.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/runtime/src/prompt.test.ts`:

```typescript
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { assembleSystemPrompt } from "./prompt.js";
import { ToolRegistry } from "@forge/tools";
import type { ContextBundle } from "@forge/types";

const emptyBundle: ContextBundle = {
  claudeMd: [],
  rules: [],
  skillDescriptions: [],
  agentDefinitions: {},
  settings: {},
};

describe("assembleSystemPrompt", () => {
  it("returns at least a base system block", () => {
    const blocks = assembleSystemPrompt(emptyBundle, new ToolRegistry(), "/tmp");
    assert.ok(blocks.length > 0);
    assert.equal(blocks[0].type, "text");
    assert.ok(blocks[0].text.length > 100, "base prompt should be substantial");
  });

  it("includes CLAUDE.md content in system-reminder tags", () => {
    const bundle: ContextBundle = {
      ...emptyBundle,
      claudeMd: [
        { path: "/project/CLAUDE.md", content: "# My Project Rules", level: "project" },
      ],
    };
    const blocks = assembleSystemPrompt(bundle, new ToolRegistry(), "/tmp");
    const allText = blocks.map((b) => b.text).join("\n");
    assert.ok(allText.includes("My Project Rules"));
    assert.ok(allText.includes("system-reminder"));
  });

  it("includes rules content", () => {
    const bundle: ContextBundle = {
      ...emptyBundle,
      rules: [
        { path: "/project/.claude/rules/style.md", content: "Use 2 space indent", level: "project" },
      ],
    };
    const blocks = assembleSystemPrompt(bundle, new ToolRegistry(), "/tmp");
    const allText = blocks.map((b) => b.text).join("\n");
    assert.ok(allText.includes("Use 2 space indent"));
  });

  it("includes skill descriptions", () => {
    const bundle: ContextBundle = {
      ...emptyBundle,
      skillDescriptions: [
        { name: "review", description: "Code review skill", path: "/p", isUserInvocable: true },
      ],
    };
    const blocks = assembleSystemPrompt(bundle, new ToolRegistry(), "/tmp");
    const allText = blocks.map((b) => b.text).join("\n");
    assert.ok(allText.includes("review"));
    assert.ok(allText.includes("Code review skill"));
  });

  it("includes environment info", () => {
    const blocks = assembleSystemPrompt(emptyBundle, new ToolRegistry(), "/tmp/workspace");
    const allText = blocks.map((b) => b.text).join("\n");
    assert.ok(allText.includes("/tmp/workspace"));
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/runtime && npx tsc && node --test dist/prompt.test.js`
Expected: FAIL

- [ ] **Step 3: Implement system prompt assembly**

Create `packages/runtime/src/prompt.ts`:

```typescript
import type { SystemBlock, ContextBundle } from "@forge/types";
import type { ToolRegistry } from "@forge/tools";

const BASE_PROMPT = `You are an AI coding assistant. You help users with software engineering tasks including writing code, debugging, refactoring, and explaining code.

Key principles:
- Read and understand existing code before suggesting modifications
- Write safe, secure, correct code — avoid introducing vulnerabilities
- Keep solutions simple and focused — only make changes that are directly requested
- Prefer editing existing files over creating new ones
- Be concise in responses

You have access to tools for reading files, writing files, editing files, running bash commands, searching code, and more. Use them to accomplish tasks.`;

export function assembleSystemPrompt(
  context: ContextBundle,
  _tools: ToolRegistry,
  cwd: string,
): SystemBlock[] {
  const blocks: SystemBlock[] = [];

  // 1. Base coding agent prompt
  blocks.push({ type: "text", text: BASE_PROMPT });

  // 2. Environment info
  const envInfo = [
    `# Environment`,
    `- Working directory: ${cwd}`,
    `- Platform: ${process.platform}`,
    `- Date: ${new Date().toISOString().slice(0, 10)}`,
  ].join("\n");
  blocks.push({ type: "text", text: envInfo });

  // 3. CLAUDE.md content
  if (context.claudeMd.length > 0) {
    const claudeMdText = context.claudeMd
      .map(
        (entry) =>
          `<system-reminder>\nContents of ${entry.path} (${entry.level} instructions):\n\n${entry.content}\n</system-reminder>`,
      )
      .join("\n\n");
    blocks.push({ type: "text", text: claudeMdText, cacheControl: { type: "ephemeral" } });
  }

  // 4. Rules
  if (context.rules.length > 0) {
    const rulesText = context.rules
      .map((r) => `<system-reminder>\nRule from ${r.path}:\n\n${r.content}\n</system-reminder>`)
      .join("\n\n");
    blocks.push({ type: "text", text: rulesText });
  }

  // 5. Skill descriptions
  if (context.skillDescriptions.length > 0) {
    const skillLines = context.skillDescriptions
      .map(
        (s) =>
          `- ${s.name}${s.isUserInvocable ? " (user-invocable)" : ""}: ${s.description}`,
      )
      .join("\n");
    blocks.push({
      type: "text",
      text: `<system-reminder>\nAvailable skills (use the Skill tool to invoke):\n\n${skillLines}\n</system-reminder>`,
    });
  }

  // 6. Agent descriptions
  const agents = Object.values(context.agentDefinitions);
  if (agents.length > 0) {
    const agentLines = agents
      .map((a) => `- ${a.name}: ${a.description}`)
      .join("\n");
    blocks.push({
      type: "text",
      text: `<system-reminder>\nAvailable agents (use the Agent tool to invoke):\n\n${agentLines}\n</system-reminder>`,
    });
  }

  return blocks;
}
```

- [ ] **Step 4: Build and run tests**

Run: `cd packages/runtime && npx tsc && node --test dist/prompt.test.js`
Expected: all 5 tests PASS

- [ ] **Step 5: Commit**

```bash
git add packages/runtime/src/prompt.ts packages/runtime/src/prompt.test.ts
git commit -m "feat(runtime): implement system prompt assembly with CLAUDE.md, rules, skills"
```

---

### Task 12: Implement SessionStore

**Files:**
- Create: `packages/runtime/src/session.ts`
- Create: `packages/runtime/src/session.test.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/runtime/src/session.test.ts`:

```typescript
import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { SessionStore } from "./session.js";

describe("SessionStore", () => {
  let dir: string;
  let store: SessionStore;

  beforeEach(() => {
    dir = join(tmpdir(), `forge-session-${randomUUID()}`);
    mkdirSync(dir, { recursive: true });
    store = new SessionStore(dir);
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("appends and loads messages", async () => {
    const sid = randomUUID();
    await store.append(sid, {
      uuid: randomUUID(),
      sessionId: sid,
      type: "user",
      message: { role: "user", content: "What is Greendale?" },
      timestamp: Date.now(),
    });
    await store.append(sid, {
      uuid: randomUUID(),
      sessionId: sid,
      type: "assistant",
      message: { role: "assistant", content: "A community college" },
      timestamp: Date.now(),
    });

    const messages = await store.load(sid);
    assert.equal(messages.length, 2);
    assert.equal(messages[0].type, "user");
    assert.equal(messages[1].type, "assistant");
  });

  it("returns empty array for nonexistent session", async () => {
    const messages = await store.load("nonexistent");
    assert.equal(messages.length, 0);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/runtime && npx tsc && node --test dist/session.test.js`
Expected: FAIL

- [ ] **Step 3: Implement SessionStore**

Create `packages/runtime/src/session.ts`:

```typescript
import { appendFile, readFile, mkdir } from "node:fs/promises";
import { join, dirname } from "node:path";
import type { SessionMessage } from "@forge/types";

export class SessionStore {
  constructor(private baseDir: string) {}

  private sessionPath(sessionId: string): string {
    return join(this.baseDir, `${sessionId}.jsonl`);
  }

  async append(sessionId: string, message: SessionMessage): Promise<void> {
    const path = this.sessionPath(sessionId);
    await mkdir(dirname(path), { recursive: true });
    await appendFile(path, JSON.stringify(message) + "\n", "utf-8");
  }

  async load(sessionId: string): Promise<SessionMessage[]> {
    const path = this.sessionPath(sessionId);
    let raw: string;
    try {
      raw = await readFile(path, "utf-8");
    } catch {
      return [];
    }

    return raw
      .split("\n")
      .filter(Boolean)
      .map((line) => JSON.parse(line) as SessionMessage);
  }
}
```

- [ ] **Step 4: Build and run tests**

Run: `cd packages/runtime && npx tsc && node --test dist/session.test.js`
Expected: all 2 tests PASS

- [ ] **Step 5: Commit**

```bash
git add packages/runtime/src/session.ts packages/runtime/src/session.test.ts
git commit -m "feat(runtime): implement JSONL SessionStore"
```

---

### Task 13: Implement ConversationLoop

**Files:**
- Create: `packages/runtime/src/loop.ts`
- Create: `packages/runtime/src/loop.test.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/runtime/src/loop.test.ts`:

```typescript
import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { ConversationLoop } from "./loop.js";
import { SessionStore } from "./session.js";
import { ToolRegistry } from "@forge/tools";
import type { LLMProvider, ChatRequest, ChatDelta, OutboundEvent, ContextBundle } from "@forge/types";

// Mock provider that returns a simple text response (no tool use)
class MockTextProvider implements LLMProvider {
  async *chat(_request: ChatRequest): AsyncIterable<ChatDelta> {
    yield { type: "text_delta", text: "E Pluribus Anus" };
    yield { type: "message_stop", stopReason: "end_turn" };
  }
}

// Mock provider that makes one tool call then responds with text
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
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/runtime && npx tsc && node --test dist/loop.test.js`
Expected: FAIL

- [ ] **Step 3: Implement ConversationLoop**

Create `packages/runtime/src/loop.ts`:

```typescript
import { randomUUID } from "node:crypto";
import type {
  LLMProvider,
  ChatRequest,
  ChatDelta,
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
    // Add user message to history
    this.history.push({
      role: "user",
      content: [{ type: "text", text: prompt }],
    });

    await this.sessionStore.append(this._sessionId, {
      uuid: randomUUID(),
      sessionId: this._sessionId,
      type: "user",
      message: { role: "user", content: prompt },
      timestamp: Date.now(),
    });

    yield* this.runLoop();
  }

  async *resume(sessionId: string, prompt: string): AsyncIterable<OutboundEvent> {
    this._sessionId = sessionId;

    // Reload history from session store
    const messages = await this.sessionStore.load(sessionId);
    for (const msg of messages) {
      const m = msg.message as { role: string; content: unknown };
      if (m.role === "user") {
        this.history.push({
          role: "user",
          content: [{ type: "text", text: m.content as string }],
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

      // Collect the full assistant message from stream
      const assistantBlocks: ChatContentBlock[] = [];
      let currentToolUse: { id: string; name: string; jsonParts: string[] } | null = null;

      for await (const delta of this.provider.chat(request)) {
        switch (delta.type) {
          case "text_delta":
            yield this.makeEvent("text", delta.text);
            // Accumulate text into the last text block or create new one
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
                  parsedInput = {};
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
            // handled after the loop
            break;
        }
      }

      // Add assistant message to history
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
        // No tool calls — done
        yield this.makeEvent("done");
        return;
      }

      // Execute tools and add results
      const toolResults: ChatContentBlock[] = [];
      for (const block of toolUseBlocks) {
        if (block.type !== "tool_use") continue;

        const ctx: ToolContext = {
          cwd: this.cwd,
          sessionId: this._sessionId,
          threadId: this.threadId,
          signal: this.signal,
          emit: (event) => {
            // Tool-emitted events are forwarded but we can't yield from here
            // This is a limitation — tools that need to stream should use a different pattern
          },
        };

        const result = await this.tools.execute(block.name, block.input, ctx);

        toolResults.push({
          type: "tool_result",
          tool_use_id: block.id,
          content: result.content as ToolResultContent[],
        });
      }

      // Add tool results as a user message (Anthropic API convention)
      this.history.push({ role: "user", content: toolResults });
    }

    // Max turns reached
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
```

- [ ] **Step 4: Build and run tests**

Run: `cd packages/runtime && npx tsc && node --test dist/loop.test.js`
Expected: all 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add packages/runtime/src/loop.ts packages/runtime/src/loop.test.ts
git commit -m "feat(runtime): implement ConversationLoop with agentic tool execution"
```

---

### Task 14: Update server to use new runtime

**Files:**
- Modify: `packages/server/src/config.ts`
- Modify: `packages/server/src/worker.ts`
- Modify: `packages/server/package.json`
- Modify: `packages/server/tsconfig.json`

- [ ] **Step 1: Add sessionsDir to config**

In `packages/server/src/config.ts`, add `sessionsDir` to the `worker` config:

```typescript
export const config = {
  gateway: {
    port: parseInt(process.env.GATEWAY_PORT || "3000", 10),
    host: process.env.GATEWAY_HOST || "0.0.0.0",
  },
  worker: {
    workspaceDir: process.env.WORKSPACE_DIR || "/tmp/forge/workspace",
    sessionsDir: process.env.SESSIONS_DIR || "/tmp/forge/sessions",
  },
} as const;
```

- [ ] **Step 2: Rewrite worker.ts**

Replace `packages/server/src/worker.ts` entirely:

```typescript
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
```

- [ ] **Step 3: Update server package.json**

Replace `packages/server/package.json` dependencies:

```json
{
  "name": "@forge/server",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "tsx watch src/index.ts",
    "build": "tsc",
    "start": "node dist/index.js"
  },
  "dependencies": {
    "@forge/types": "*",
    "@forge/tools": "*",
    "@forge/runtime": "*",
    "fastify": "^5.2.0"
  },
  "devDependencies": {
    "@types/node": "^22.0.0",
    "tsx": "^4.19.0",
    "typescript": "^5.7.0"
  }
}
```

- [ ] **Step 4: Update server tsconfig.json references**

```json
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": {
    "outDir": "dist",
    "rootDir": "src"
  },
  "include": ["src/**/*"],
  "references": [
    { "path": "../types" },
    { "path": "../tools" },
    { "path": "../runtime" }
  ]
}
```

- [ ] **Step 5: Update root build scripts**

Update root `package.json` build script:

```json
"build": "npm run build -w @forge/types && npm run build -w @forge/tools && npm run build -w @forge/runtime && npm run build -w @forge/server && npm run build -w @forge/cli"
```

Update `justfile` `dev-server` recipe:

```just
dev-server: build-types build-tools build-runtime
  npm run dev -w @forge/server
```

- [ ] **Step 6: Install dependencies and build**

Run: `npm install && just build`
Expected: clean compile, no errors

- [ ] **Step 7: Update server/src/index.ts to create sessions dir**

In `packages/server/src/index.ts`, add sessions dir creation:

```typescript
import { mkdirSync } from "node:fs";
import { config } from "./config.js";
import { startGateway } from "./gateway.js";

async function main(): Promise<void> {
  mkdirSync(config.worker.workspaceDir, { recursive: true });
  mkdirSync(config.worker.sessionsDir, { recursive: true });
  console.log("forge server starting...");
  console.log(`  workspace: ${config.worker.workspaceDir}`);
  console.log(`  sessions:  ${config.worker.sessionsDir}`);
  await startGateway();
}

main().catch((err) => {
  console.error("fatal:", err);
  process.exit(1);
});
```

- [ ] **Step 8: Build everything and verify**

Run: `just build`
Expected: clean compile, zero errors across all packages

- [ ] **Step 9: Commit**

```bash
git add packages/server/ package.json tsconfig.json justfile
git commit -m "feat(server): replace Agent SDK with @forge/runtime + @forge/tools"
```

---

### Task 15: Verify end-to-end (manual smoke test)

**Files:** None (manual testing only)

- [ ] **Step 1: Set ANTHROPIC_API_KEY**

```bash
export ANTHROPIC_API_KEY="your-key-here"
```

- [ ] **Step 2: Start the server**

Run: `just dev-server`
Expected: "forge server starting..." with workspace and sessions paths

- [ ] **Step 3: In another terminal, start the CLI**

Run: `just dev-cli`
Expected: connected, prompt appears

- [ ] **Step 4: Send a simple message**

Type: `What is 2 + 2?`
Expected: text response streamed back, "done" event received

- [ ] **Step 5: Send a tool-using message**

Type: `Create a file at /tmp/forge/workspace/test.txt with the content "six seasons and a movie"`
Expected: tool_use events for Write tool, file created on disk

- [ ] **Step 6: Verify the file**

Run: `cat /tmp/forge/workspace/test.txt`
Expected: "six seasons and a movie"

- [ ] **Step 7: Verify session persistence**

Run: `ls /tmp/forge/sessions/`
Expected: JSONL file(s) present

---

That's the plan. Tasks 1-14 are the implementation, Task 15 is the smoke test. Each task is self-contained with TDD (test first, implement, verify, commit).
