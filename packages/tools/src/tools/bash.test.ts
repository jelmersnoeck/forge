import { test } from "node:test";
import { strictEqual, ok } from "node:assert";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { bashTool } from "./bash.js";
import type { ToolContext } from "@forge/types";

const testDir = join(tmpdir(), `forge-bash-test-${Date.now()}`);

function makeCtx(cwd = testDir): ToolContext {
  return {
    cwd,
    sessionId: "test-session",
    threadId: "test-thread",
    signal: new AbortController().signal,
    emit: () => {},
  };
}

test("Bash: runs echo command", async () => {
  await mkdir(testDir, { recursive: true });

  const result = await bashTool.handler(
    { command: 'echo "Hello from Greendale"' },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("Hello from Greendale"));

  await rm(testDir, { recursive: true });
});

test("Bash: captures stderr on failure", async () => {
  await mkdir(testDir, { recursive: true });

  const result = await bashTool.handler(
    { command: "ls /nonexistent-troy-barnes" },
    makeCtx(),
  );

  strictEqual(result.isError, true);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.length > 0);

  await rm(testDir, { recursive: true });
});

test("Bash: respects cwd", async () => {
  await mkdir(testDir, { recursive: true });

  const result = await bashTool.handler({ command: "pwd" }, makeCtx());

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes(testDir));

  await rm(testDir, { recursive: true });
});

test("Bash: timeout handling kills process", async () => {
  await mkdir(testDir, { recursive: true });

  const start = Date.now();
  const result = await bashTool.handler(
    { command: "sleep 10", timeout: 500 },
    makeCtx(),
  );
  const elapsed = Date.now() - start;

  // Should terminate much sooner than 10s
  ok(elapsed < 2000);
  ok(result.content[0].type === "text");

  await rm(testDir, { recursive: true });
});

test("Bash: spawn error returns error result", async () => {
  await mkdir(testDir, { recursive: true });

  const result = await bashTool.handler(
    { command: "/nonexistent-binary-dean-pelton" },
    makeCtx(),
  );

  // bash will run and return non-zero exit for bad command
  strictEqual(result.isError, true);

  await rm(testDir, { recursive: true });
});
