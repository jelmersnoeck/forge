import type { ContextBundle } from "@forge/types";

export class ContextLoader {
  constructor(private cwd: string) {}
  async load(_sources: ("user" | "project" | "local")[]): Promise<ContextBundle> {
    return { claudeMd: [], rules: [], skillDescriptions: [], agentDefinitions: {}, settings: {} };
  }
  async loadSkillContent(_name: string): Promise<string> {
    throw new Error("Not implemented");
  }
}
