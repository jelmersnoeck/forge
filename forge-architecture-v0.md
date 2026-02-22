# Forge v0 Architecture

## Core Idea

Forge is a Go binary that runs as CLI or server. Same engine, two frontends. Every capability is an interface — agents, trackers, environments are all pluggable backends.

```
┌─────────────────────────────────────────────────────┐
│                    Frontends                         │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐  │
│  │   CLI    │  │  Server  │  │  Webhook Handlers  │  │
│  │ (sync)   │  │  (API)   │  │  (GitHub/Jira/...) │  │
│  └────┬─────┘  └────┬─────┘  └────────┬──────────┘  │
│       └──────────────┼─────────────────┘             │
│                      ▼                               │
│              ┌───────────────┐                        │
│              │  Forge Engine │                        │
│              └───────┬───────┘                        │
│                      │                               │
│    ┌─────────┬───────┼────────┬──────────┐           │
│    ▼         ▼       ▼        ▼          ▼           │
│ ┌──────┐ ┌───────┐ ┌──────┐ ┌─────┐ ┌────────┐     │
│ │Agent │ │Tracker│ │Review│ │ Env │ │Planner │     │
│ │ i/f  │ │  i/f  │ │Engine│ │ i/f │ │        │     │
│ └──┬───┘ └──┬────┘ └──────┘ └──┬──┘ └────────┘     │
│    │        │                   │                    │
└────┼────────┼───────────────────┼────────────────────┘
     │        │                   │
     ▼        ▼                   ▼
  Backends  Backends           Backends
```

---

## Interfaces

### Agent Interface

The agent does the actual LLM work — planning, coding, reviewing. Forge doesn't care how.

```go
type Agent interface {
    // Run executes a prompt with given permissions and returns structured output.
    // This is the only method. Everything else is prompt engineering.
    Run(ctx context.Context, req AgentRequest) (*AgentResponse, error)
}

type AgentRequest struct {
    Prompt      string            // The assembled prompt
    WorkDir     string            // Working directory (repo checkout)
    Mode        AgentMode         // plan | code | review
    Permissions ToolPermissions   // What the agent can do
    OutputFormat string           // json | text | stream
    Model       string            // Optional model override
}

type AgentMode string
const (
    ModePlan   AgentMode = "plan"   // Read-only, outputs structured plan
    ModeCode   AgentMode = "code"   // Full access, writes code, runs tests
    ModeReview AgentMode = "review" // Read-only, outputs structured findings
)

type ToolPermissions struct {
    Read    bool   // Can read files
    Write   bool   // Can write files
    Execute bool   // Can run commands
    Network bool   // Can make network calls
}
```

**Backends:**

| Backend | How it works | Subscription? |
|---------|-------------|--------------|
| `claude-code` | Shells out to `claude -p "..." --output-format json` | Yes — Max plan works |
| `opencode` | Shells out to `opencode -p "..." -f json` | No — API keys only |
| `custom` | HTTP call to any server implementing the Agent interface | Depends on provider |

**Claude Code backend (v0 default):**

```go
type ClaudeCodeAgent struct {
    Binary string // path to `claude` binary
}

func (a *ClaudeCodeAgent) Run(ctx context.Context, req AgentRequest) (*AgentResponse, error) {
    args := []string{
        "-p", req.Prompt,
        "--output-format", req.OutputFormat,
    }
    if req.Permissions.Write {
        args = append(args, "--allowedTools", "Edit,Write,Bash")
    }
    // shell out, capture stdout, parse response
    cmd := exec.CommandContext(ctx, a.Binary, args...)
    cmd.Dir = req.WorkDir
    // ...
}
```

**OpenCode backend:**

```go
type OpenCodeAgent struct {
    Binary string
    Model  string // e.g. "claude-sonnet-4-20250514" via API key
}

func (a *OpenCodeAgent) Run(ctx context.Context, req AgentRequest) (*AgentResponse, error) {
    args := []string{"-p", req.Prompt, "-f", "json"}
    if a.Model != "" {
        args = append(args, "--model", a.Model)
    }
    cmd := exec.CommandContext(ctx, a.Binary, args...)
    cmd.Dir = req.WorkDir
    // ...
}
```

**Why this matters:** You can use Claude Code on Max plan for cheap daily use, OpenCode with Gemini API for CI (where you're burning tokens on every PR), and eventually custom fine-tuned reviewers for specific principle domains.

---

### Tracker Interface

Forge needs to read work items and write results. Different orgs use different trackers.

```go
type Tracker interface {
    // GetIssue fetches a work item by reference
    GetIssue(ctx context.Context, ref string) (*Issue, error)
    
    // CreateIssue creates a new work item (used by planner)
    CreateIssue(ctx context.Context, issue *CreateIssueRequest) (*Issue, error)
    
    // CreatePR submits completed work
    CreatePR(ctx context.Context, pr *CreatePRRequest) (*PullRequest, error)
    
    // Comment adds status updates
    Comment(ctx context.Context, ref string, body string) error
    
    // Link sets dependencies between issues (used by planner)
    Link(ctx context.Context, from string, to string, rel LinkRelation) error
}

type Issue struct {
    ID          string
    Tracker     string   // "github" | "jira" | "linear"
    Title       string
    Body        string
    Labels      []string
    Repo        string   // for GitHub
    Project     string   // for Jira/Linear
    DependsOn   []string // issue refs this blocks on
    Status      string
    URL         string
}
```

**Issue references** — the `--issue` flag takes a URI:

```bash
# GitHub
forge build --issue github://org/repo#123
forge build --issue gh:org/repo#123          # shorthand

# Jira
forge build --issue jira://PROJECT-456
forge build --issue jira:PROJECT-456         # shorthand

# Linear
forge build --issue linear://TEAM-789
forge build --issue lin:TEAM-789             # shorthand

# Local spec (no tracker, just a markdown file)
forge build --issue file://./specs/feature.md
forge build --issue ./specs/feature.md       # implicit file://
```

**URI parsing:**

```go
func ParseIssueRef(ref string) (*IssueRef, error) {
    // Try explicit scheme first
    // "github://org/repo#123" → tracker=github, org=org, repo=repo, id=123
    // "jira://PROJECT-456"    → tracker=jira, project=PROJECT, id=456
    // "linear://TEAM-789"     → tracker=linear, team=TEAM, id=789
    
    // Shorthand detection
    // "gh:org/repo#123"       → github
    // "PROJECT-456"           → looks like Jira (CAPS-NUMBER pattern)
    // "#123"                  → github, uses repo from cwd or config
    // "./spec.md"             → file
}
```

**Config-level defaults:**

```yaml
# .forge/config.yaml
tracker:
  default: github
  github:
    org: mycompany
    default_repo: main-service  # so you can just do: forge build --issue #123
  jira:
    instance: mycompany.atlassian.net
    project: PLATFORM
  linear:
    team: engineering
```

---

### Environment Interface

Where code gets deployed and tested. Pluggable for different infra setups.

```go
type Environment interface {
    Provision(ctx context.Context, spec EnvSpec) (*Env, error)
    Deploy(ctx context.Context, env *Env, artifacts []Artifact) error
    Test(ctx context.Context, env *Env, tests TestSpec) (*TestResult, error)
    Teardown(ctx context.Context, env *Env) error
}
```

**v0:** `docker-compose` or `k3d` locally. Later: vCluster, cloud K8s, Fly.io.

Not the focus of v0. The agent interface and tracker interface are.

---

## CLI / Server Duality

### CLI Mode

Synchronous. User watches, approves, intervenes.

```bash
# Single issue — full governed loop
forge build --issue gh:org/repo#123

# Review only — run on existing diff (CI use case)
forge review --diff HEAD~1..HEAD --principles security,architecture

# Plan only — decompose issue, output plan for approval
forge plan --issue gh:org/repo#123

# Workstream — decompose goal into issues, execute in order
forge plan --goal "Migrate auth to OAuth2" --tracker github --repo org/repo
forge build --workstream ws-2026-02-21-001
```

### Server Mode

Same engine, HTTP API. Enables webhooks, async execution, dashboards.

```bash
forge serve --port 8080 --config .forge/config.yaml
```

**API:**

```
POST /api/v1/build
  { "issue": "github://org/repo#123" }
  → Returns job ID, streams progress via SSE

POST /api/v1/review
  { "diff": "<unified diff>", "principles": ["security"] }
  → Returns findings synchronously

POST /api/v1/plan
  { "goal": "Add multi-tenant support", "tracker": "github", "repo": "org/repo" }
  → Returns workstream plan for approval

POST /api/v1/plan/execute
  { "workstream_id": "ws-001", "approved": true }
  → Creates issues, starts execution

GET  /api/v1/jobs/:id
  → Job status, logs, findings

GET  /api/v1/jobs/:id/stream
  → SSE stream of job progress
```

**Webhook endpoints:**

```
POST /webhooks/github
  → issue.opened with label "forge" → triggers build
  → pull_request.opened → triggers review
  → issue_comment with "/forge build" → triggers build

POST /webhooks/jira
  → issue transitioned to "Ready for Forge" → triggers build

POST /webhooks/linear
  → issue labeled "forge" → triggers build
```

**Implementation — same engine:**

```go
// core engine — doesn't know about HTTP or CLI
type Engine struct {
    agents      map[string]Agent
    trackers    map[string]Tracker
    principles  *PrincipleStore
    envs        map[string]Environment
}

func (e *Engine) Build(ctx context.Context, req BuildRequest) (*BuildResult, error) {
    // 1. Fetch issue from tracker
    // 2. Load principles
    // 3. Run plan agent
    // 4. Run code agent
    // 5. Run review agents (parallel)
    // 6. Evaluate findings → loop or proceed
    // 7. Submit PR via tracker
}

// CLI frontend
func cmdBuild(cmd *cobra.Command, args []string) error {
    engine := buildEngine(cfg)
    result, err := engine.Build(ctx, BuildRequest{Issue: issueFlag})
    // print progress to terminal
}

// Server frontend
func handleBuild(w http.ResponseWriter, r *http.Request) {
    var req BuildRequest
    json.NewDecoder(r.Body).Decode(&req)
    jobID := jobs.Start(func() { engine.Build(ctx, req) })
    json.NewEncoder(w).Encode(map[string]string{"job_id": jobID})
}
```

---

## The Planner Layer

The planner sits above the build loop. It takes a high-level goal and decomposes it into a workstream of issues that can be executed in dependency order.

### What a Workstream Looks Like

```yaml
# Generated by: forge plan --goal "Add rate limiting to API"
workstream:
  id: ws-2026-02-21-001
  goal: "Add rate limiting to API"
  tracker: github
  repo: org/api-service
  created: 2026-02-21T10:30:00Z
  
  phases:
    - name: "Infrastructure"
      issues:
        - ref: pending      # not yet created in tracker
          title: "Add Redis connection pool and health checks"
          description: |
            Set up Redis client with connection pooling for rate limit
            state storage. Include health check endpoint.
          labels: ["forge", "infrastructure"]
          depends_on: []
          
        - ref: pending
          title: "Create rate limit middleware with sliding window"
          description: |
            Implement token bucket or sliding window rate limiter
            as HTTP middleware. Configurable per-route limits.
          labels: ["forge", "infrastructure"]
          depends_on: ["Add Redis connection pool and health checks"]
    
    - name: "Integration"
      issues:
        - ref: pending
          title: "Apply rate limits to public API endpoints"
          description: |
            Configure and apply rate limiting middleware to all
            public-facing API routes. Set appropriate limits per tier.
          labels: ["forge", "api"]
          depends_on: ["Create rate limit middleware with sliding window"]
          
        - ref: pending
          title: "Add rate limit headers and 429 responses"
          description: |
            Return X-RateLimit-* headers on all responses. Return
            429 Too Many Requests with Retry-After when exceeded.
          labels: ["forge", "api"]
          depends_on: ["Apply rate limits to public API endpoints"]
    
    - name: "Observability"
      issues:
        - ref: pending
          title: "Add rate limit metrics and alerting"
          description: |
            Export rate limit hit/miss metrics to Prometheus.
            Alert when rejection rate exceeds threshold.
          labels: ["forge", "observability"]
          depends_on: ["Apply rate limits to public API endpoints"]
```

### Workstream Execution

```bash
# Step 1: Generate the plan
forge plan --goal "Add rate limiting to API" \
  --tracker github --repo org/api-service \
  --context ./docs/architecture.md \
  --context ./docs/api-design.md

# Step 2: Human reviews the workstream YAML, edits if needed
# (opens in editor, or reviews in UI when running as server)

# Step 3: Create issues in tracker
forge plan --execute --workstream ws-2026-02-21-001
# → Creates GitHub issues, sets dependencies via labels/links
# → Each issue gets a "forge" label and workstream ID in body

# Step 4: Execute the workstream
forge build --workstream ws-2026-02-21-001
# → Resolves dependency graph
# → Executes issues in topological order
# → Parallel where dependencies allow
# → Each issue goes through full governed build loop
# → Shared context: previous issues' plans/code inform next issue
```

### Workstream Agent Spawning

```
forge build --workstream ws-001

Dependency graph:
  Redis setup ──┐
                ├──▶ Rate limit middleware ──▶ Apply to endpoints ──┬──▶ Headers/429
                                                                    └──▶ Metrics

Execution:
  t=0   [Agent 1] Redis setup (no deps, starts immediately)
  t=1   [Agent 1] Rate limit middleware (Redis done, starts)
  t=2   [Agent 1] Apply to endpoints (middleware done, starts)
  t=3   [Agent 1] Headers/429     }  (endpoints done, both start)
        [Agent 2] Metrics/alerting }  ← parallel, separate agents
```

Each agent gets:
- The issue description
- The principles
- Shared context from previous issues in the workstream (plans, key decisions)
- The full repo state (including code from previous issues, already merged to workstream branch)

```go
type WorkstreamExecutor struct {
    engine *Engine
    agents []Agent  // pool of agents for parallel execution
}

func (w *WorkstreamExecutor) Execute(ctx context.Context, ws *Workstream) error {
    graph := buildDependencyGraph(ws)
    
    for {
        // Find issues whose dependencies are all complete
        ready := graph.Ready()
        if len(ready) == 0 {
            break // all done or deadlocked
        }
        
        // Execute ready issues in parallel (up to agent pool size)
        var wg sync.WaitGroup
        for _, issue := range ready {
            wg.Add(1)
            agent := w.agents.Acquire()
            go func(iss *Issue) {
                defer wg.Done()
                defer w.agents.Release(agent)
                
                // Build shared context from completed predecessors
                sharedCtx := w.buildSharedContext(graph.Completed())
                
                result := w.engine.Build(ctx, BuildRequest{
                    Issue:        iss,
                    SharedContext: sharedCtx,
                    Branch:       ws.Branch(), // workstream branch
                })
                
                graph.MarkComplete(iss, result)
            }(issue)
        }
        wg.Wait()
    }
    
    // All issues complete → create umbrella PR for workstream
    return w.engine.CreateWorkstreamPR(ctx, ws, graph)
}
```

---

## Build Loop (The Governed Workflow)

This is the core — what happens for a single issue.

```
                    ┌──────────────────────────────────────────────┐
                    │              BUILD LOOP                       │
                    │                                              │
  Issue ──▶ PLAN ──▶ [Human Approval] ──▶ CODE ──▶ REVIEW ──┐    │
              │                            ▲          │       │    │
              │                            │          ▼       │    │
              │                            │      Findings    │    │
              │                            │          │       │    │
              │                            │    Critical? ────┘    │
              │                            │      Yes → loop       │
              │                            │      No  → PR         │
              │                            │                       │
              │                            └── (max 3 iterations)  │
              │                                                    │
              └────────────────────────────────────────────────────┘

  Where agents are used:
    PLAN   → Agent(mode=plan, permissions=read-only)
    CODE   → Agent(mode=code, permissions=read+write+execute)
    REVIEW → Agent(mode=review, permissions=read-only) × N reviewers
```

### Review is the product

The review step is where governance lives:

```go
func (e *Engine) Review(ctx context.Context, req ReviewRequest) (*ReviewResult, error) {
    principles := e.principles.Load(req.PrincipleSets...)
    
    // Assemble review prompt with:
    // - The diff
    // - All applicable principles (with IDs, severity, rationale)
    // - Structured output format (file, line, principle ID, severity, fix)
    prompt := assembleReviewPrompt(req.Diff, principles)
    
    // Run N reviewers in parallel (different principle domains)
    var findings []Finding
    var wg sync.WaitGroup
    
    for _, reviewer := range req.Reviewers {
        wg.Add(1)
        go func(r ReviewerConfig) {
            defer wg.Done()
            resp, _ := e.agents[r.Agent].Run(ctx, AgentRequest{
                Prompt:      prompt,
                WorkDir:     req.WorkDir,
                Mode:        ModeReview,
                Permissions: ToolPermissions{Read: true},
            })
            findings = append(findings, parseFindings(resp)...)
        }(reviewer)
    }
    wg.Wait()
    
    return &ReviewResult{
        Findings:     findings,
        HasCritical:  hasCritical(findings),
        PrinciplesCovered: principlesCovered(findings, principles),
    }, nil
}
```

---

## Configuration

```yaml
# .forge/config.yaml

# Agent configuration
agent:
  default: claude-code
  backends:
    claude-code:
      binary: claude
      # uses Max plan via OAuth — no API key needed
    opencode:
      binary: opencode
      model: claude-sonnet-4-20250514
      # requires ANTHROPIC_API_KEY env var
    opencode-gemini:
      binary: opencode
      model: gemini-2.5-pro
      # requires GOOGLE_API_KEY env var

  # Which agent to use for which role
  roles:
    planner: claude-code          # best reasoning for decomposition
    coder: claude-code            # Max plan for heavy lifting
    reviewer: opencode-gemini     # cheaper model for CI review

# Tracker configuration
tracker:
  default: github
  github:
    org: mycompany
    default_repo: main-service
  jira:
    instance: mycompany.atlassian.net
    default_project: PLATFORM
    auth: env:JIRA_API_TOKEN
  linear:
    team: engineering
    auth: env:LINEAR_API_KEY

# Principle configuration
principles:
  paths:
    - .forge/principles/         # local principles
    - ~/.forge/principles/       # user-level principles
  active:
    - security
    - architecture
    - simplicity

# Build loop configuration
build:
  max_iterations: 3
  branch_pattern: "forge/{{.Tracker}}-{{.IssueID}}"
  workstream_branch_pattern: "forge/ws-{{.WorkstreamID}}"
  test_command: "make test"
  require_plan_approval: true    # human must approve plan in CLI mode
  
  # Review configuration
  review:
    parallel_reviewers: 2
    reviewer_agent: claude-code   # separate sessions = fresh eyes
    severity_threshold: warning   # block PR on warning+, or just critical

# Server configuration (only used with `forge serve`)
server:
  port: 8080
  webhooks:
    github:
      secret: env:GITHUB_WEBHOOK_SECRET
      triggers:
        - event: issues.opened
          label: forge
          action: build
        - event: pull_request.opened
          action: review
        - event: issue_comment.created
          pattern: "/forge build"
          action: build
    jira:
      auth: env:JIRA_WEBHOOK_SECRET
      triggers:
        - transition_to: "Ready for Forge"
          action: build
```

---

## Project Structure

```
forge/
├── cmd/
│   └── forge/
│       ├── main.go
│       ├── build.go           # forge build
│       ├── review.go          # forge review
│       ├── plan.go            # forge plan
│       ├── serve.go           # forge serve
│       └── workstream.go      # forge build --workstream
│
├── internal/
│   ├── engine/
│   │   ├── engine.go          # core Build/Review/Plan orchestration
│   │   ├── loop.go            # governed build loop with iteration
│   │   └── workstream.go      # dependency graph execution
│   │
│   ├── agent/
│   │   ├── agent.go           # Agent interface
│   │   ├── claudecode.go      # Claude Code backend
│   │   ├── opencode.go        # OpenCode backend
│   │   └── http.go            # Generic HTTP agent backend
│   │
│   ├── tracker/
│   │   ├── tracker.go         # Tracker interface
│   │   ├── github.go          # GitHub Issues + PRs
│   │   ├── jira.go            # Jira
│   │   ├── linear.go          # Linear
│   │   ├── file.go            # Local markdown spec
│   │   └── ref.go             # URI parser for issue refs
│   │
│   ├── principles/
│   │   ├── store.go           # Load, merge, version principles
│   │   ├── schema.go          # Principle YAML schema
│   │   └── prompt.go          # Assemble principles into prompts
│   │
│   ├── review/
│   │   ├── review.go          # Review orchestration
│   │   ├── findings.go        # Finding schema, severity, dedup
│   │   └── prompt.go          # Assemble review prompts
│   │
│   ├── env/
│   │   ├── env.go             # Environment interface
│   │   ├── docker.go          # docker-compose backend
│   │   └── k3d.go             # k3d backend
│   │
│   └── server/
│       ├── server.go          # HTTP server, routes
│       ├── api.go             # REST API handlers
│       ├── webhooks.go        # Webhook handlers (GitHub, Jira, Linear)
│       ├── jobs.go            # Job queue, status tracking
│       └── sse.go             # Server-sent events for streaming
│
├── pkg/
│   └── config/
│       └── config.go          # .forge/config.yaml parsing
│
└── prompts/
    ├── plan.md                # Plan agent system prompt
    ├── code.md                # Code agent system prompt
    ├── review.md              # Review agent system prompt
    └── workstream.md          # Workstream planner system prompt
```

---

## What Gets Built When

### Phase 0 — Validate (now, Claude Code skills)
Already done. `.claude/commands/` files. Test if principles catch real issues.

### Phase 1 — Minimal CLI
- `Agent` interface + Claude Code backend
- `Tracker` interface + GitHub backend only
- `PrincipleStore` — load YAML, assemble prompts
- `forge build --issue gh:org/repo#123` — full governed loop
- `forge review --diff` — standalone review
- Single agent, sequential execution
- No server, no webhooks, no planner

### Phase 2 — CI Integration
- `forge review` as GitHub Action
- OpenCode backend (for API-key CI environments)
- Structured output: SARIF format for GitHub code scanning integration
- This is the wedge product

### Phase 3 — Server + Webhooks
- `forge serve` — HTTP API
- GitHub webhook handler
- Job queue (SQLite initially)
- SSE streaming
- Basic web dashboard for job status

### Phase 4 — Planner + Workstreams
- Workstream planning from goals
- Issue creation in trackers
- Dependency graph execution
- Parallel agent spawning
- Shared context between issues
- Jira + Linear tracker backends

### Phase 5 — Environments
- Docker-compose environment backend
- k3d environment backend
- Deploy + e2e test in governed loop
- vCluster for production-like environments

---

## Key Design Decisions

**Why Go?** Single binary, easy cross-compilation, good concurrency primitives for parallel agents, no runtime dependencies. User installs one binary.

**Why shell out to agents instead of SDK?** Agent CLIs already handle auth, model selection, tool permissions, streaming. Wrapping their SDK means reimplementing all of that. Shelling out to `claude` or `opencode` means we get Max plan auth for free, model updates for free, new tools for free. The interface boundary is stdin/stdout.

**Why tracker as interface?** Every org has a different source of truth. GitHub-only would limit adoption. The abstraction is thin — fetch issue, create PR, add comment. The planner needs it to create issues and set dependencies.

**Why server mode matters early?** Webhooks are how this becomes autonomous. Issue gets created → Forge picks it up → builds → creates PR. No human in the loop except plan approval (which can be async via PR comment). The server is also the foundation for dashboards, audit trails, and team features.

**Why workstream planning is Phase 4, not Phase 1?** Single-issue governance is the thesis to validate. If principle-based review doesn't catch real issues, workstream planning is premature. Validate the atom before building the molecule.
