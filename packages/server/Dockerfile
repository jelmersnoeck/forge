# Build context must be the monorepo root:
#   docker build -f packages/server/Dockerfile .
FROM node:22-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends git ripgrep curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Install workspace deps — copy root + all package.jsons for layer caching.
COPY package.json package-lock.json ./
COPY packages/types/package.json ./packages/types/
COPY packages/tools/package.json ./packages/tools/
COPY packages/runtime/package.json ./packages/runtime/
COPY packages/server/package.json ./packages/server/
RUN npm ci --omit=dev

# Copy built source.
COPY packages/types/dist/ ./packages/types/dist/
COPY packages/tools/dist/ ./packages/tools/dist/
COPY packages/runtime/dist/ ./packages/runtime/dist/
COPY packages/server/dist/ ./packages/server/dist/

ENV NODE_ENV=production
CMD ["node", "packages/server/dist/index.js"]
