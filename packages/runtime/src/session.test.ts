import { test } from "node:test";
import { strict as assert } from "node:assert";
import { SessionStore } from "./session.js";
import { rm, mkdir } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import type { SessionMessage } from "@forge/types";

test("SessionStore appends and loads messages", async () => {
  const testDir = join(tmpdir(), `session-test-${Date.now()}`);
  await mkdir(testDir, { recursive: true });

  const store = new SessionStore(testDir);
  const sessionId = "test-session-1";

  const message1: SessionMessage = {
    uuid: "msg-1",
    sessionId,
    type: "user",
    message: { role: "user", content: "Hello" },
    timestamp: Date.now(),
  };

  const message2: SessionMessage = {
    uuid: "msg-2",
    parentUuid: "msg-1",
    sessionId,
    type: "assistant",
    message: { role: "assistant", content: "Hi there!" },
    timestamp: Date.now(),
  };

  await store.append(sessionId, message1);
  await store.append(sessionId, message2);

  const loaded = await store.load(sessionId);

  assert.equal(loaded.length, 2);
  assert.equal(loaded[0].uuid, "msg-1");
  assert.equal(loaded[0].type, "user");
  assert.equal(loaded[1].uuid, "msg-2");
  assert.equal(loaded[1].type, "assistant");
  assert.equal(loaded[1].parentUuid, "msg-1");

  await rm(testDir, { recursive: true, force: true });
});

test("SessionStore returns empty array for nonexistent session", async () => {
  const testDir = join(tmpdir(), `session-test-${Date.now()}`);
  await mkdir(testDir, { recursive: true });

  const store = new SessionStore(testDir);
  const loaded = await store.load("nonexistent-session");

  assert.deepEqual(loaded, []);

  await rm(testDir, { recursive: true, force: true });
});

test("SessionStore creates directory if it doesn't exist", async () => {
  const testDir = join(tmpdir(), `session-test-${Date.now()}`, "nested", "path");

  const store = new SessionStore(testDir);
  const sessionId = "test-session-2";

  const message: SessionMessage = {
    uuid: "msg-1",
    sessionId,
    type: "user",
    message: { role: "user", content: "Test" },
    timestamp: Date.now(),
  };

  await store.append(sessionId, message);
  const loaded = await store.load(sessionId);

  assert.equal(loaded.length, 1);
  assert.equal(loaded[0].uuid, "msg-1");

  await rm(join(tmpdir(), `session-test-${Date.now().toString().slice(0, -3)}`), {
    recursive: true,
    force: true,
  });
});
