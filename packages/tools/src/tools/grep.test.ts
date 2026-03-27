import { test } from "node:test";
import { strictEqual, ok } from "node:assert";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { grepTool } from "./grep.js";
import type { ToolContext } from "@forge/types";

const testDir = join(tmpdir(), `forge-grep-test-${Date.now()}`);

function makeCtx(cwd = testDir): ToolContext {
  return {
    cwd,
    sessionId: "test-session",
    threadId: "test-thread",
    signal: new AbortController().signal,
    emit: () => {},
  };
}

test("Grep: finds files matching pattern (default files_with_matches)", async () => {
  await mkdir(testDir, { recursive: true });
  await writeFile(join(testDir, "troy.txt"), "Troy Barnes is cool");
  await writeFile(join(testDir, "abed.txt"), "Abed Nadir is awesome");
  await writeFile(join(testDir, "jeff.txt"), "Jeff Winger is boring");

  const result = await grepTool.handler({ pattern: "cool|awesome" }, makeCtx());

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  const text = result.content[0].text;
  ok(text.includes("troy.txt"));
  ok(text.includes("abed.txt"));
  ok(!text.includes("jeff.txt"));

  await rm(testDir, { recursive: true });
});

test("Grep: content mode shows matching text", async () => {
  await mkdir(testDir, { recursive: true });
  await writeFile(join(testDir, "greendale.txt"), "Welcome to Greendale\nHome of the Human Being");

  const result = await grepTool.handler(
    { pattern: "Greendale", output_mode: "content" },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("Welcome to Greendale"));

  await rm(testDir, { recursive: true });
});

test("Grep: no matches returns graceful message", async () => {
  await mkdir(testDir, { recursive: true });
  await writeFile(join(testDir, "test.txt"), "nothing here");

  const result = await grepTool.handler(
    { pattern: "nonexistent-pattern-chang" },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  strictEqual(result.content[0].text, "(no matches)");

  await rm(testDir, { recursive: true });
});

test("Grep: case insensitive search", async () => {
  await mkdir(testDir, { recursive: true });
  await writeFile(join(testDir, "case.txt"), "GREENDALE community college");

  const result = await grepTool.handler(
    { pattern: "greendale", output_mode: "content", "-i": true },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("GREENDALE"));

  await rm(testDir, { recursive: true });
});

test("Grep: glob filter", async () => {
  await mkdir(testDir, { recursive: true });
  await writeFile(join(testDir, "test.ts"), "export const dean = 'Pelton'");
  await writeFile(join(testDir, "test.js"), "const dean = 'Pelton'");

  const result = await grepTool.handler(
    { pattern: "dean", glob: "*.ts" },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("test.ts"));
  ok(!result.content[0].text.includes("test.js"));

  await rm(testDir, { recursive: true });
});

test("Grep: head_limit restricts output", async () => {
  await mkdir(testDir, { recursive: true });
  await writeFile(
    join(testDir, "multi.txt"),
    "line1\nline2\nline3\nline4\nline5",
  );

  const result = await grepTool.handler(
    { pattern: "line", output_mode: "content", head_limit: 2 },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  const lines = result.content[0].text.trim().split("\n");
  strictEqual(lines.length, 2);

  await rm(testDir, { recursive: true });
});
