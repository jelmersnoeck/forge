import { test } from "node:test";
import { strictEqual, ok } from "node:assert";
import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { editTool } from "./edit.js";
import type { ToolContext } from "@forge/types";

const testDir = join(tmpdir(), `forge-edit-test-${Date.now()}`);

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

test("Edit: replaces unique string", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "test.txt");
  await writeFile(file, "Hello Troy Barnes!\nWelcome to Greendale.\n");

  const result = await editTool.handler(
    {
      file_path: file,
      old_string: "Troy Barnes",
      new_string: "Abed Nadir",
    },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("replaced"));

  const contents = await readFile(file, "utf-8");
  strictEqual(contents, "Hello Abed Nadir!\nWelcome to Greendale.\n");

  await rm(testDir, { recursive: true });
});

test("Edit: returns error when old_string not found", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "test.txt");
  await writeFile(file, "Hello Troy Barnes!\n");

  const result = await editTool.handler(
    {
      file_path: file,
      old_string: "Jeff Winger",
      new_string: "Dean Pelton",
    },
    makeCtx(),
  );

  strictEqual(result.isError, true);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("not found"));

  await rm(testDir, { recursive: true });
});

test("Edit: returns error when old_string appears multiple times without replace_all", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "test.txt");
  await writeFile(
    file,
    "Troy loves Troy. Troy is Troy. Troy Troy Troy.\n",
  );

  const result = await editTool.handler(
    {
      file_path: file,
      old_string: "Troy",
      new_string: "Abed",
    },
    makeCtx(),
  );

  strictEqual(result.isError, true);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("appears 7 times"));
  ok(result.content[0].text.includes("not unique"));

  await rm(testDir, { recursive: true });
});

test("Edit: replaces all occurrences when replace_all is true", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "test.txt");
  await writeFile(
    file,
    "Troy loves Troy. Troy is Troy. Troy Troy Troy.\n",
  );

  const result = await editTool.handler(
    {
      file_path: file,
      old_string: "Troy",
      new_string: "Abed",
      replace_all: true,
    },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("7 occurrences"));

  const contents = await readFile(file, "utf-8");
  strictEqual(
    contents,
    "Abed loves Abed. Abed is Abed. Abed Abed Abed.\n",
  );

  await rm(testDir, { recursive: true });
});

test("Edit: handles multiline replacements", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "test.txt");
  await writeFile(
    file,
    "function hello() {\n  console.log('old');\n}\n",
  );

  const result = await editTool.handler(
    {
      file_path: file,
      old_string: "function hello() {\n  console.log('old');\n}",
      new_string: "function hello() {\n  console.log('new');\n}",
    },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);

  const contents = await readFile(file, "utf-8");
  strictEqual(
    contents,
    "function hello() {\n  console.log('new');\n}\n",
  );

  await rm(testDir, { recursive: true });
});

test("Edit: returns error for nonexistent file", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "nonexistent.txt");

  const result = await editTool.handler(
    {
      file_path: file,
      old_string: "foo",
      new_string: "bar",
    },
    makeCtx(),
  );

  strictEqual(result.isError, true);
  ok(result.content[0].type === "text");
  ok(result.content[0].text.includes("does not exist"));

  await rm(testDir, { recursive: true });
});

test("Edit: preserves file when old_string and new_string are identical", async () => {
  await mkdir(testDir, { recursive: true });
  const file = join(testDir, "test.txt");
  const original = "Human Being mascot\n";
  await writeFile(file, original);

  const result = await editTool.handler(
    {
      file_path: file,
      old_string: "mascot",
      new_string: "mascot",
    },
    makeCtx(),
  );

  strictEqual(result.isError, undefined);

  const contents = await readFile(file, "utf-8");
  strictEqual(contents, original);

  await rm(testDir, { recursive: true });
});
