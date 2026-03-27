import { test } from "node:test";
import { strict as assert } from "node:assert";
import { ContextLoader } from "./context.js";
import { mkdir, writeFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";

test("ContextLoader loads CLAUDE.md from project root", async () => {
  const testDir = join(tmpdir(), `context-test-${Date.now()}`);
  await mkdir(testDir, { recursive: true });

  const claudeMdPath = join(testDir, "CLAUDE.md");
  await writeFile(claudeMdPath, "# Project instructions");

  const loader = new ContextLoader(testDir);
  const bundle = await loader.load(["project"]);

  assert.equal(bundle.claudeMd.length, 1);
  assert.equal(bundle.claudeMd[0].content, "# Project instructions");
  assert.equal(bundle.claudeMd[0].level, "project");

  await rm(testDir, { recursive: true, force: true });
});

test("ContextLoader loads CLAUDE.md from .claude/ directory", async () => {
  const testDir = join(tmpdir(), `context-test-${Date.now()}`);
  await mkdir(join(testDir, ".claude"), { recursive: true });

  const claudeMdPath = join(testDir, ".claude", "CLAUDE.md");
  await writeFile(claudeMdPath, "# .claude instructions");

  const loader = new ContextLoader(testDir);
  const bundle = await loader.load(["project"]);

  assert.equal(bundle.claudeMd.length, 1);
  assert.equal(bundle.claudeMd[0].content, "# .claude instructions");
  assert.equal(bundle.claudeMd[0].level, "project");

  await rm(testDir, { recursive: true, force: true });
});

test("ContextLoader loads rules from .claude/rules/", async () => {
  const testDir = join(tmpdir(), `context-test-${Date.now()}`);
  await mkdir(join(testDir, ".claude", "rules"), { recursive: true });

  await writeFile(join(testDir, ".claude", "rules", "security.md"), "# Security rules");
  await writeFile(join(testDir, ".claude", "rules", "style.md"), "# Style rules");

  const loader = new ContextLoader(testDir);
  const bundle = await loader.load(["project"]);

  assert.equal(bundle.rules.length, 2);
  assert.ok(bundle.rules.some((r) => r.content === "# Security rules"));
  assert.ok(bundle.rules.some((r) => r.content === "# Style rules"));

  await rm(testDir, { recursive: true, force: true });
});

test("ContextLoader discovers skills with descriptions from frontmatter", async () => {
  const testDir = join(tmpdir(), `context-test-${Date.now()}`);
  await mkdir(join(testDir, ".claude", "skills", "test-skill"), { recursive: true });

  const skillContent = `---
name: test-skill
description: A test skill for unit tests
isUserInvocable: true
---

# Test Skill

This is the body of the test skill.
`;
  await writeFile(join(testDir, ".claude", "skills", "test-skill", "SKILL.md"), skillContent);

  const loader = new ContextLoader(testDir);
  const bundle = await loader.load(["project"]);

  assert.equal(bundle.skillDescriptions.length, 1);
  assert.equal(bundle.skillDescriptions[0].name, "test-skill");
  assert.equal(bundle.skillDescriptions[0].description, "A test skill for unit tests");
  assert.equal(bundle.skillDescriptions[0].isUserInvocable, true);

  await rm(testDir, { recursive: true, force: true });
});

test("ContextLoader loads skill content lazily via loadSkillContent()", async () => {
  const testDir = join(tmpdir(), `context-test-${Date.now()}`);
  await mkdir(join(testDir, ".claude", "skills", "lazy-skill"), { recursive: true });

  const skillContent = `---
name: lazy-skill
description: Lazy loaded skill
---

# Lazy Skill Body

This content should only be loaded when requested.
`;
  await writeFile(join(testDir, ".claude", "skills", "lazy-skill", "SKILL.md"), skillContent);

  const loader = new ContextLoader(testDir);
  const content = await loader.loadSkillContent("lazy-skill");

  assert.ok(content.includes("# Lazy Skill Body"));
  assert.ok(content.includes("This content should only be loaded when requested."));

  await rm(testDir, { recursive: true, force: true });
});

test("ContextLoader throws when loading nonexistent skill", async () => {
  const testDir = join(tmpdir(), `context-test-${Date.now()}`);
  await mkdir(testDir, { recursive: true });

  const loader = new ContextLoader(testDir);
  await assert.rejects(
    async () => await loader.loadSkillContent("nonexistent-skill"),
    /Skill not found/,
  );

  await rm(testDir, { recursive: true, force: true });
});

test("ContextLoader loads CLAUDE.local.md when local source included", async () => {
  const testDir = join(tmpdir(), `context-test-${Date.now()}`);
  await mkdir(testDir, { recursive: true });

  await writeFile(join(testDir, "CLAUDE.local.md"), "# Local overrides");

  const loader = new ContextLoader(testDir);
  const bundle = await loader.load(["local"]);

  assert.equal(bundle.claudeMd.length, 1);
  assert.equal(bundle.claudeMd[0].content, "# Local overrides");
  assert.equal(bundle.claudeMd[0].level, "local");

  await rm(testDir, { recursive: true, force: true });
});

test("ContextLoader discovers agent definitions from .claude/agents/", async () => {
  const testDir = join(tmpdir(), `context-test-${Date.now()}`);
  await mkdir(join(testDir, ".claude", "agents"), { recursive: true });

  const agentContent = `---
name: test-agent
description: A test agent
model: sonnet
tools: ["read", "write"]
maxTurns: 10
---

You are a test agent. Follow instructions carefully.
`;
  await writeFile(join(testDir, ".claude", "agents", "test-agent.md"), agentContent);

  const loader = new ContextLoader(testDir);
  const bundle = await loader.load(["project"]);

  assert.ok(bundle.agentDefinitions["test-agent"]);
  assert.equal(bundle.agentDefinitions["test-agent"].name, "test-agent");
  assert.equal(bundle.agentDefinitions["test-agent"].description, "A test agent");
  assert.equal(bundle.agentDefinitions["test-agent"].model, "sonnet");
  assert.deepEqual(bundle.agentDefinitions["test-agent"].tools, ["read", "write"]);
  assert.equal(bundle.agentDefinitions["test-agent"].maxTurns, 10);
  assert.ok(bundle.agentDefinitions["test-agent"].prompt.includes("You are a test agent"));

  await rm(testDir, { recursive: true, force: true });
});

test("ContextLoader merges settings from .claude/settings.json", async () => {
  const testDir = join(tmpdir(), `context-test-${Date.now()}`);
  await mkdir(join(testDir, ".claude"), { recursive: true });

  const settings = {
    model: "claude-sonnet-4-5",
    permissions: { allow: ["read"], deny: ["write"] },
  };
  await writeFile(join(testDir, ".claude", "settings.json"), JSON.stringify(settings));

  const loader = new ContextLoader(testDir);
  const bundle = await loader.load(["project"]);

  assert.equal(bundle.settings.model, "claude-sonnet-4-5");
  assert.deepEqual(bundle.settings.permissions, { allow: ["read"], deny: ["write"] });

  await rm(testDir, { recursive: true, force: true });
});
