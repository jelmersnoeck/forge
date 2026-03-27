# Build context must be the monorepo root:
#   docker build -f packages/server/Dockerfile .
FROM node:22-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends git curl python3 \
    && npm install -g @anthropic-ai/claude-code \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Install workspace deps — copies root + all package.jsons for layer caching.
COPY package.json package-lock.json ./
COPY packages/types/package.json ./packages/types/
COPY packages/server/package.json ./packages/server/
RUN npm ci --omit=dev -w @forge/types -w @forge/server

# Copy built source.
COPY packages/types/dist/ ./packages/types/dist/
COPY packages/server/dist/ ./packages/server/dist/

ENV NODE_ENV=production
CMD ["node", "packages/server/dist/index.js"]
