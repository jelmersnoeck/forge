import { mkdirSync } from "node:fs";
import { config } from "./config.js";
import { startGateway } from "./gateway.js";

async function main(): Promise<void> {
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
