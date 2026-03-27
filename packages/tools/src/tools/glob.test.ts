import { test } from "node:test";
import { strictEqual, ok } from "node:assert";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { globTool } from "./glob.js";
import type { ToolContext } from "@forge/types";

const testDir = join(tmpdir(), `forge-glob-test-${Date.now()}`);

function makeCtx(cwd = testDir): ToolContext {
  return {
    cwd,
    sessionId: "test-session",
    threadId: "test-thread",
    signal: new AbortController().signal,
    emit: () => {},
  };
}

test("Glob: finds *.ts files", async () => {
  await mkdir(testDir, { recursive: true });
  await writeFile(join(testDir, "abed.ts"), "// Abed Nadir");
  await writeFile(join(testDir, "troy.ts"), "// Troy Barnes");
  await writeFile(join(testDir, "annie.js"), "// Annie Edison");

  const result = await globTool.handler({ pattern: "*.ts" }, makeCtx());

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  const text = result.content[0].text;
  ok(text.includes("abed.ts"));
  ok(text.includes("troy.ts"));
  ok(!text.includes("annie.js"));

  await rm(testDir, { recursive: true });
});

test("Glob: uses custom path", async () => {
  await mkdir(testDir, { recursive: true });
  const subdir = join(testDir, "study-room");
  await mkdir(subdir, { recursive: true });
  await writeFile(join(subdir, "britta.md"), "# Britta Perry");
  await writeFile(join(testDir, "dean.md"), "# Dean Pelton");

  const result = await globTool.handler(
    { pattern: "*.md", path: subdir },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  const text = result.content[0].text;
  ok(text.includes("britta.md"));
  ok(!text.includes("dean.md"));

  await rm(testDir, { recursive: true });
});

test("Glob: no matches returns graceful message", async () => {
  await mkdir(testDir, { recursive: true });

  const result = await globTool.handler(
    { pattern: "*.nonexistent" },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  strictEqual(result.content[0].text, "(no matches)");

  await rm(testDir, { recursive: true });
});

test("Glob: sorts by modification time (newest first)", async () => {
  await mkdir(testDir, { recursive: true });

  // Write files with slight delays to ensure different mtimes
  await writeFile(join(testDir, "old.txt"), "old");
  await new Promise((resolve) => setTimeout(resolve, 10));
  await writeFile(join(testDir, "new.txt"), "new");

  const result = await globTool.handler({ pattern: "*.txt" }, makeCtx());

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  const lines = result.content[0].text.split("\n");
  // Newest file should be first
  ok(lines[0].includes("new.txt"));
  ok(lines[1].includes("old.txt"));

  await rm(testDir, { recursive: true });
});
