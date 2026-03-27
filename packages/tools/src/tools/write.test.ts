import { test } from "node:test";
import { strictEqual, ok } from "node:assert";
import { readFile, mkdir, rm, access } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { writeTool } from "./write.js";
import type { ToolContext } from "@forge/types";

const testDir = join(tmpdir(), `forge-write-test-${Date.now()}`);

// Helper to create test context
function makeCtx(): ToolContext {
  return {
    cwd: testDir,
    sessionId: "test-session",
    threadId: "test-thread",
    signal: new AbortController().signal,
    emit: () => {},
  };
}

test("Write: creates new file with content", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "new-file.txt");

  const result = await writeTool.handler(
    { file_path: file, content: "Hello, Troy Barnes!" },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  strictEqual(result.content.length, 1);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("19 bytes"));

  // Verify file was actually created
  const contents = await readFile(file, "utf-8");
  strictEqual(contents, "Hello, Troy Barnes!");

  await rm(testDir, { recursive: true });
});

test("Write: creates parent directories", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "deep", "nested", "dir", "file.txt");

  const result = await writeTool.handler(
    { file_path: file, content: "Greendale Community College" },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("27 bytes"));

  // Verify file exists
  const contents = await readFile(file, "utf-8");
  strictEqual(contents, "Greendale Community College");

  await rm(testDir, { recursive: true });
});

test("Write: overwrites existing file", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "overwrite.txt");

  // Write initial content
  await writeTool.handler(
    { file_path: file, content: "Original content" },
    makeCtx(),
  );

  // Overwrite
  const result = await writeTool.handler(
    { file_path: file, content: "New content from Señor Chang" },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("29 bytes"));

  // Verify new content
  const contents = await readFile(file, "utf-8");
  strictEqual(contents, "New content from Señor Chang");

  await rm(testDir, { recursive: true });
});

test("Write: handles empty content", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "empty.txt");

  const result = await writeTool.handler(
    { file_path: file, content: "" },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("0 bytes"));

  // Verify file exists and is empty
  const contents = await readFile(file, "utf-8");
  strictEqual(contents, "");

  await rm(testDir, { recursive: true });
});

test("Write: returns error on write failure", async () => {
  // Try to write to a path that can't be created (e.g., root without permissions)
  const invalidPath = "/this/path/should/not/be/writable/test.txt";

  const result = await writeTool.handler(
    { file_path: invalidPath, content: "test" },
    makeCtx(),
  );

  strictEqual(result.isError, true);
  ok(result.content[0].type === "text");
  ok(
    result.content[0].text.includes("Failed to write") ||
      result.content[0].text.includes("EACCES") ||
      result.content[0].text.includes("EPERM"),
  );
});
