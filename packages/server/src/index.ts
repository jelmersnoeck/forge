import { mkdirSync, readFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";
import { config } from "./config.js";
import { startGateway } from "./gateway.js";

// Load .env from project root (walk up from server package to monorepo root).
// No external deps — just read the file and shove vars into process.env.
function loadEnv(): void {
  // Try cwd first, then two levels up (packages/server -> root)
  for (const base of [process.cwd(), resolve(import.meta.dirname, "../../..")]) {
    const envPath = resolve(base, ".env");
    if (!existsSync(envPath)) continue;

    const lines = readFileSync(envPath, "utf-8").split("\n");
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith("#")) continue;
      const eq = trimmed.indexOf("=");
      if (eq < 0) continue;
      const key = trimmed.slice(0, eq).trim();
      const val = trimmed.slice(eq + 1).trim();
      // Don't override existing env vars (explicit exports win)
      if (!(key in process.env)) {
        process.env[key] = val;
      }
    }
    return;
  }
}

async function main(): Promise<void> {
  loadEnv();

  if (!process.env.ANTHROPIC_API_KEY) {
    console.error("fatal: ANTHROPIC_API_KEY not set");
    console.error("  set it in .env or export it in your shell");
    process.exit(1);
  }

  mkdirSync(config.worker.workspaceDir, { recursive: true });
  mkdirSync(config.worker.sessionsDir, { recursive: true });

  console.log("forge server starting...");
  console.log(`  workspace: ${config.worker.workspaceDir}`);
  console.log(`  sessions:  ${config.worker.sessionsDir}`);

  await startGateway();
}

main().catch((err) => {
  console.error("fatal:", err);
  process.exit(1);
});
