import type {
  ContextBundle,
  ClaudeMdEntry,
  RuleEntry,
  SkillDescription,
  AgentDefinition,
  MergedSettings,
} from "@forge/types";
import { readFile, readdir, access } from "node:fs/promises";
import { join, dirname } from "node:path";
import { homedir } from "node:os";

interface ParsedFrontmatter {
  frontmatter: Record<string, unknown>;
  body: string;
}

// Parse YAML frontmatter from markdown: ---\nkey: value\n---\nbody
function parseFrontmatter(raw: string): ParsedFrontmatter {
  const lines = raw.split("\n");
  if (lines[0]?.trim() !== "---") {
    return { frontmatter: {}, body: raw };
  }

  const endIdx = lines.findIndex((line, i) => i > 0 && line.trim() === "---");
  if (endIdx === -1) {
    return { frontmatter: {}, body: raw };
  }

  const frontmatterLines = lines.slice(1, endIdx);
  const body = lines.slice(endIdx + 1).join("\n");

  const frontmatter: Record<string, unknown> = {};
  for (const line of frontmatterLines) {
    const colonIdx = line.indexOf(":");
    if (colonIdx === -1) continue;
    const key = line.slice(0, colonIdx).trim();
    const value = line.slice(colonIdx + 1).trim();
    // Basic type coercion: true/false → boolean, numbers → number, arrays, rest → string
    if (value === "true") {
      frontmatter[key] = true;
    } else if (value === "false") {
      frontmatter[key] = false;
    } else if (/^\d+$/.test(value)) {
      frontmatter[key] = parseInt(value, 10);
    } else if (value.startsWith("[") && value.endsWith("]")) {
      // Try parsing as JSON array
      try {
        frontmatter[key] = JSON.parse(value);
      } catch {
        frontmatter[key] = value;
      }
    } else {
      // Remove quotes if present
      frontmatter[key] = value.replace(/^["']|["']$/g, "");
    }
  }

  return { frontmatter, body };
}

async function tryReadFile(path: string): Promise<string | null> {
  try {
    return await readFile(path, "utf-8");
  } catch {
    return null;
  }
}

async function tryReadDir(path: string): Promise<string[] | null> {
  try {
    return await readdir(path);
  } catch {
    return null;
  }
}

async function exists(path: string): Promise<boolean> {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}

export class ContextLoader {
  constructor(private cwd: string) {}

  async load(sources: ("user" | "project" | "local")[]): Promise<ContextBundle> {
    const bundle: ContextBundle = {
      claudeMd: [],
      rules: [],
      skillDescriptions: [],
      agentDefinitions: {},
      settings: {},
    };

    const settingsStack: Record<string, unknown>[] = [];

    // 1. User level
    if (sources.includes("user")) {
      const userDir = join(homedir(), ".claude");
      await this.loadUserLevel(userDir, bundle, settingsStack);
    }

    // 2. Parent directories (walk upward from cwd)
    if (sources.includes("project")) {
      await this.loadParentDirs(this.cwd, bundle);
    }

    // 3. Project level
    if (sources.includes("project")) {
      await this.loadProjectLevel(this.cwd, bundle, settingsStack);
    }

    // 4. Local level
    if (sources.includes("local")) {
      await this.loadLocalLevel(this.cwd, bundle, settingsStack);
    }

    // Merge settings: later sources override earlier
    for (const settings of settingsStack) {
      bundle.settings = { ...bundle.settings, ...settings };
    }

    return bundle;
  }

  async loadSkillContent(name: string): Promise<string> {
    // Try project skills first
    const projectSkillPath = join(this.cwd, ".claude", "skills", name, "SKILL.md");
    const projectContent = await tryReadFile(projectSkillPath);
    if (projectContent) {
      return projectContent;
    }

    // Try user skills
    const userSkillPath = join(homedir(), ".claude", "skills", name, "SKILL.md");
    const userContent = await tryReadFile(userSkillPath);
    if (userContent) {
      return userContent;
    }

    throw new Error(`Skill not found: ${name}`);
  }

  private async loadUserLevel(
    userDir: string,
    bundle: ContextBundle,
    settingsStack: Record<string, unknown>[],
  ): Promise<void> {
    // CLAUDE.md
    const claudeMdPath = join(userDir, "CLAUDE.md");
    const claudeMd = await tryReadFile(claudeMdPath);
    if (claudeMd) {
      bundle.claudeMd.push({ path: claudeMdPath, content: claudeMd, level: "user" });
    }

    // rules/*.md
    const rulesDir = join(userDir, "rules");
    const ruleFiles = await tryReadDir(rulesDir);
    if (ruleFiles) {
      for (const file of ruleFiles) {
        if (!file.endsWith(".md")) continue;
        const path = join(rulesDir, file);
        const content = await tryReadFile(path);
        if (content) {
          bundle.rules.push({ path, content, level: "user" });
        }
      }
    }

    // skills/*/SKILL.md (lazy load, only frontmatter)
    const skillsDir = join(userDir, "skills");
    const skillDirs = await tryReadDir(skillsDir);
    if (skillDirs) {
      for (const skillName of skillDirs) {
        const skillPath = join(skillsDir, skillName, "SKILL.md");
        const skillContent = await tryReadFile(skillPath);
        if (skillContent) {
          const { frontmatter } = parseFrontmatter(skillContent);
          bundle.skillDescriptions.push({
            name: (frontmatter.name as string) || skillName,
            description: (frontmatter.description as string) || "",
            path: skillPath,
            isUserInvocable: (frontmatter.isUserInvocable as boolean) ?? true,
          });
        }
      }
    }

    // settings.json
    const settingsPath = join(userDir, "settings.json");
    const settings = await tryReadFile(settingsPath);
    if (settings) {
      try {
        settingsStack.push(JSON.parse(settings));
      } catch {}
    }
  }

  private async loadParentDirs(startDir: string, bundle: ContextBundle): Promise<void> {
    let current = startDir;
    const visited = new Set<string>();

    while (true) {
      const parent = dirname(current);
      if (parent === current || visited.has(parent)) break;
      visited.add(parent);

      const claudeMdPath = join(parent, "CLAUDE.md");
      const claudeMd = await tryReadFile(claudeMdPath);
      if (claudeMd) {
        bundle.claudeMd.push({ path: claudeMdPath, content: claudeMd, level: "parent" });
      }

      current = parent;
    }
  }

  private async loadProjectLevel(
    cwd: string,
    bundle: ContextBundle,
    settingsStack: Record<string, unknown>[],
  ): Promise<void> {
    // CLAUDE.md (root or .claude/)
    const rootClaudeMd = join(cwd, "CLAUDE.md");
    const dotClaudeMd = join(cwd, ".claude", "CLAUDE.md");

    const rootContent = await tryReadFile(rootClaudeMd);
    if (rootContent) {
      bundle.claudeMd.push({ path: rootClaudeMd, content: rootContent, level: "project" });
    }

    const dotContent = await tryReadFile(dotClaudeMd);
    if (dotContent) {
      bundle.claudeMd.push({ path: dotClaudeMd, content: dotContent, level: "project" });
    }

    // .claude/rules/*.md
    const rulesDir = join(cwd, ".claude", "rules");
    const ruleFiles = await tryReadDir(rulesDir);
    if (ruleFiles) {
      for (const file of ruleFiles) {
        if (!file.endsWith(".md")) continue;
        const path = join(rulesDir, file);
        const content = await tryReadFile(path);
        if (content) {
          bundle.rules.push({ path, content, level: "project" });
        }
      }
    }

    // .claude/skills/*/SKILL.md
    const skillsDir = join(cwd, ".claude", "skills");
    const skillDirs = await tryReadDir(skillsDir);
    if (skillDirs) {
      for (const skillName of skillDirs) {
        const skillPath = join(skillsDir, skillName, "SKILL.md");
        const skillContent = await tryReadFile(skillPath);
        if (skillContent) {
          const { frontmatter } = parseFrontmatter(skillContent);
          bundle.skillDescriptions.push({
            name: (frontmatter.name as string) || skillName,
            description: (frontmatter.description as string) || "",
            path: skillPath,
            isUserInvocable: (frontmatter.isUserInvocable as boolean) ?? true,
          });
        }
      }
    }

    // .claude/agents/*.md
    const agentsDir = join(cwd, ".claude", "agents");
    const agentFiles = await tryReadDir(agentsDir);
    if (agentFiles) {
      for (const file of agentFiles) {
        if (!file.endsWith(".md")) continue;
        const path = join(agentsDir, file);
        const content = await tryReadFile(path);
        if (!content) continue;

        const { frontmatter, body } = parseFrontmatter(content);
        const name = (frontmatter.name as string) || file.replace(/\.md$/, "");
        const agentDef: AgentDefinition = {
          name,
          description: (frontmatter.description as string) || "",
          prompt: body.trim(),
          tools: frontmatter.tools as string[] | undefined,
          disallowedTools: frontmatter.disallowedTools as string[] | undefined,
          model: frontmatter.model as AgentDefinition["model"] | undefined,
          maxTurns: frontmatter.maxTurns as number | undefined,
        };
        bundle.agentDefinitions[name] = agentDef;
      }
    }

    // .claude/settings.json
    const settingsPath = join(cwd, ".claude", "settings.json");
    const settings = await tryReadFile(settingsPath);
    if (settings) {
      try {
        settingsStack.push(JSON.parse(settings));
      } catch {}
    }
  }

  private async loadLocalLevel(
    cwd: string,
    bundle: ContextBundle,
    settingsStack: Record<string, unknown>[],
  ): Promise<void> {
    // CLAUDE.local.md
    const localClaudeMd = join(cwd, "CLAUDE.local.md");
    const localContent = await tryReadFile(localClaudeMd);
    if (localContent) {
      bundle.claudeMd.push({ path: localClaudeMd, content: localContent, level: "local" });
    }

    // .claude/settings.local.json
    const localSettingsPath = join(cwd, ".claude", "settings.local.json");
    const localSettings = await tryReadFile(localSettingsPath);
    if (localSettings) {
      try {
        settingsStack.push(JSON.parse(localSettings));
      } catch {}
    }
  }
}
