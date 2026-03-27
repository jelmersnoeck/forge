import type { SessionMessage } from "@forge/types";
import { appendFile, readFile, mkdir } from "node:fs/promises";
import { join, dirname } from "node:path";

export class SessionStore {
  constructor(private baseDir: string) {}

  async append(sessionId: string, message: SessionMessage): Promise<void> {
    const sessionPath = join(this.baseDir, `${sessionId}.jsonl`);

    // Ensure directory exists
    await mkdir(dirname(sessionPath), { recursive: true });

    const line = JSON.stringify(message) + "\n";
    await appendFile(sessionPath, line, "utf-8");
  }

  async load(sessionId: string): Promise<SessionMessage[]> {
    const sessionPath = join(this.baseDir, `${sessionId}.jsonl`);

    try {
      const content = await readFile(sessionPath, "utf-8");
      const lines = content.trim().split("\n").filter((line) => line.length > 0);
      return lines.map((line) => JSON.parse(line) as SessionMessage);
    } catch (err) {
      // File doesn't exist or can't be read - return empty array
      if ((err as NodeJS.ErrnoException).code === "ENOENT") {
        return [];
      }
      throw err;
    }
  }
}
