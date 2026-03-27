import { test } from "node:test";
import { strict as assert } from "node:assert";
import { assembleSystemPrompt } from "./prompt.js";
import { ToolRegistry } from "@forge/tools";
import type { ContextBundle } from "@forge/types";

test("assembleSystemPrompt returns at least a base system block", () => {
  const emptyContext: ContextBundle = {
    claudeMd: [],
    rules: [],
    skillDescriptions: [],
    agentDefinitions: {},
    settings: {},
  };

  const registry = new ToolRegistry();
  const blocks = assembleSystemPrompt(emptyContext, registry, "/tmp/test");

  assert.ok(blocks.length >= 2); // Base prompt + env info
  assert.equal(blocks[0].type, "text");
  assert.ok(blocks[0].text.includes("coding assistant"));
});

test("assembleSystemPrompt includes CLAUDE.md content in system-reminder tags", () => {
  const context: ContextBundle = {
    claudeMd: [
      {
        path: "/project/CLAUDE.md",
        content: "# Project instructions\n\nBe concise.",
        level: "project",
      },
    ],
    rules: [],
    skillDescriptions: [],
    agentDefinitions: {},
    settings: {},
  };

  const registry = new ToolRegistry();
  const blocks = assembleSystemPrompt(context, registry, "/project");

  const claudeBlock = blocks.find((b) => b.text.includes("<system-reminder>"));
  assert.ok(claudeBlock);
  assert.ok(claudeBlock.text.includes("# Project instructions"));
  assert.ok(claudeBlock.text.includes("Be concise."));
  assert.equal(claudeBlock.cacheControl?.type, "ephemeral");
});

test("assembleSystemPrompt includes rules content", () => {
  const context: ContextBundle = {
    claudeMd: [],
    rules: [
      {
        path: "/project/.claude/rules/security.md",
        content: "# Security\n\nNever log secrets.",
        level: "project",
      },
    ],
    skillDescriptions: [],
    agentDefinitions: {},
    settings: {},
  };

  const registry = new ToolRegistry();
  const blocks = assembleSystemPrompt(context, registry, "/project");

  const rulesBlock = blocks.find((b) => b.text.includes("Rules:"));
  assert.ok(rulesBlock);
  assert.ok(rulesBlock.text.includes("# Security"));
  assert.ok(rulesBlock.text.includes("Never log secrets."));
  assert.equal(rulesBlock.cacheControl?.type, "ephemeral");
});

test("assembleSystemPrompt includes skill descriptions", () => {
  const context: ContextBundle = {
    claudeMd: [],
    rules: [],
    skillDescriptions: [
      {
        name: "test-skill",
        description: "A test skill for unit tests",
        path: "/skills/test-skill/SKILL.md",
        isUserInvocable: true,
      },
      {
        name: "debug-skill",
        description: "Debug complex issues",
        path: "/skills/debug-skill/SKILL.md",
        isUserInvocable: false,
      },
    ],
    agentDefinitions: {},
    settings: {},
  };

  const registry = new ToolRegistry();
  const blocks = assembleSystemPrompt(context, registry, "/project");

  const skillsBlock = blocks.find((b) => b.text.includes("Available skills:"));
  assert.ok(skillsBlock);
  assert.ok(skillsBlock.text.includes("test-skill: A test skill for unit tests"));
  assert.ok(skillsBlock.text.includes("debug-skill: Debug complex issues"));
});

test("assembleSystemPrompt includes environment info (cwd)", () => {
  const context: ContextBundle = {
    claudeMd: [],
    rules: [],
    skillDescriptions: [],
    agentDefinitions: {},
    settings: {},
  };

  const registry = new ToolRegistry();
  const blocks = assembleSystemPrompt(context, registry, "/Users/troy/greendale");

  const envBlock = blocks.find((b) => b.text.includes("Working directory:"));
  assert.ok(envBlock);
  assert.ok(envBlock.text.includes("/Users/troy/greendale"));
  assert.ok(envBlock.text.includes("Platform:"));
  assert.ok(envBlock.text.includes("Date:"));
});

test("assembleSystemPrompt includes agent descriptions", () => {
  const context: ContextBundle = {
    claudeMd: [],
    rules: [],
    skillDescriptions: [],
    agentDefinitions: {
      "test-agent": {
        name: "test-agent",
        description: "A test agent for unit tests",
        prompt: "You are a test agent.",
        tools: ["read", "write"],
        model: "sonnet",
      },
      "review-agent": {
        name: "review-agent",
        description: "Reviews code for quality issues",
        prompt: "You are a review agent.",
        model: "opus",
      },
    },
    settings: {},
  };

  const registry = new ToolRegistry();
  const blocks = assembleSystemPrompt(context, registry, "/project");

  const agentsBlock = blocks.find((b) => b.text.includes("Available agents:"));
  assert.ok(agentsBlock);
  assert.ok(agentsBlock.text.includes("test-agent: A test agent for unit tests"));
  assert.ok(agentsBlock.text.includes("review-agent: Reviews code for quality issues"));
});
