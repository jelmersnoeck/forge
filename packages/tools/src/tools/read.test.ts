import { test } from "node:test";
import { strictEqual, deepStrictEqual, ok } from "node:assert";
import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { readTool } from "./read.js";
import type { ToolContext } from "@forge/types";

const testDir = join(tmpdir(), `forge-read-test-${Date.now()}`);

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

test("Read: reads text file with line numbers", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "test.txt");
  await writeFile(file, "line one\nline two\nline three\n");

  const result = await readTool.handler({ file_path: file }, makeCtx());

  strictEqual(result.isError, undefined);
  strictEqual(result.content.length, 1);
  strictEqual(result.content[0].type, "text");
  ok(result.content[0].type === "text");
  const text = result.content[0].text;
  ok(text.includes("1\tline one"));
  ok(text.includes("2\tline two"));
  ok(text.includes("3\tline three"));

  await rm(testDir, { recursive: true });
});

test("Read: respects offset and limit", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "test-offset.txt");
  await writeFile(
    file,
    "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\n",
  );

  const result = await readTool.handler(
    { file_path: file, offset: 3, limit: 3 },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  strictEqual(result.content.length, 1);
  ok(result.content[0].type === "text");
  const text = result.content[0].text;
  ok(text.includes("3\tline3"));
  ok(text.includes("4\tline4"));
  ok(text.includes("5\tline5"));
  ok(!text.includes("2\tline2"));
  ok(!text.includes("6\tline6"));

  await rm(testDir, { recursive: true });
});

test("Read: returns error for nonexistent file", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "nonexistent.txt");

  const result = await readTool.handler({ file_path: file }, makeCtx());

  strictEqual(result.isError, true);
  strictEqual(result.content.length, 1);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("does not exist"));

  await rm(testDir, { recursive: true });
});

test("Read: returns base64 for image files", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "test.png");
  // Write a tiny fake PNG (just random bytes for testing)
  const fakeImage = Buffer.from([
    137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13,
  ]);
  await writeFile(file, fakeImage);

  const result = await readTool.handler({ file_path: file }, makeCtx());

  strictEqual(result.isError, undefined);
  strictEqual(result.content.length, 1);
  strictEqual(result.content[0].type, "image");
  ok(result.content[0].type === "image");
  strictEqual(result.content[0].source.type, "base64");
  strictEqual(result.content[0].source.media_type, "image/png");
  ok(result.content[0].source.data.length > 0);

  await rm(testDir, { recursive: true });
});

test("Read: detects various image extensions", async () => {
  await mkdir(testDir, { recursive: true });
  const extensions = ["jpg", "jpeg", "gif", "webp", "svg"];

  for (const ext of extensions) {
    const file = join(testDir, `test.${ext}`);
    await writeFile(file, Buffer.from([1, 2, 3, 4]));

    const result = await readTool.handler({ file_path: file }, makeCtx());

    strictEqual(result.isError, undefined);
    strictEqual(result.content[0].type, "image");
  }

  await rm(testDir, { recursive: true });
});
