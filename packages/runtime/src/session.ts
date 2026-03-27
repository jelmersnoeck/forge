import type { SessionMessage } from "@forge/types";

export class SessionStore {
  constructor(private baseDir: string) {}
  async append(_sessionId: string, _message: SessionMessage): Promise<void> {}
  async load(_sessionId: string): Promise<SessionMessage[]> { return []; }
}
