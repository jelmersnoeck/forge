export const config = {
  gateway: {
    port: parseInt(process.env.GATEWAY_PORT || "3000", 10),
    host: process.env.GATEWAY_HOST || "0.0.0.0",
  },
  worker: {
    workspaceDir: process.env.WORKSPACE_DIR || "/tmp/forge/workspace",
  },
} as const;
