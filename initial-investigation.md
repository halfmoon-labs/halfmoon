# PicoClaw Codebase Investigation

An in-depth investigation of the PicoClaw codebase — an ultra-lightweight personal AI assistant written in Go, designed to run on $10 hardware with <10MB RAM.

---

## Table of Contents

1. [Project Overview](#project-overview)
2. [Architecture & Tech Stack](#architecture--tech-stack)
3. [The Agent Loop — Core Reasoning Engine](#the-agent-loop--core-reasoning-engine)
4. [Tool System](#tool-system)
5. [Web Search — Provider Abstraction](#web-search--provider-abstraction)
6. [LLM Response Contract](#llm-response-contract)
7. [Multi-Agent System](#multi-agent-system)
8. [Sub-Agent Identity & Isolation](#sub-agent-identity--isolation)
9. [Observability & Extension Points](#observability--extension-points)
10. [Secrets & Environment Variables](#secrets--environment-variables)
11. [Heartbeat System](#heartbeat-system)
12. [Cron System](#cron-system)
13. [Code Quality Summary](#code-quality-summary)

---

## Project Overview

PicoClaw is a fork of an open-source project by Sipeed. It is a personal AI assistant that supports 15+ chat platforms (Telegram, Discord, Slack, Matrix, IRC, Feishu, DingTalk, WeChat, etc.), 6+ LLM providers (OpenAI, Anthropic, AWS Bedrock, Azure, Google Gemini, GitHub Copilot), and a React-based web console.

**Key metrics:**
- Target: <10MB RAM, <1s boot on 0.6GHz single-core
- Pure Go (`CGO_ENABLED=0`), no C dependencies
- Multi-arch: amd64, arm64, riscv64, mipsle, loong64
- Current version: v0.2.3 (March 2026)

---

## Architecture & Tech Stack

### Backend (Core)

| Component | Technology |
|-----------|-----------|
| Language | Go 1.25+ |
| CLI | Cobra |
| Database | SQLite 3 (modernc.org/sqlite, pure Go) |
| Logging | zerolog (structured) |
| Config | JSON + YAML + TOML |
| Build | Makefile + GoReleaser |

### Frontend (Web Console)

| Component | Technology |
|-----------|-----------|
| Language | TypeScript 5.9+ |
| Framework | React 19.2 |
| Build | Vite 7.3+ |
| Router | TanStack Router |
| State | Jotai (atomic state) |
| Data Fetching | TanStack React Query |
| Styling | Tailwind CSS 4.2+ |
| UI | Radix UI, shadcn |
| i18n | i18next |

### Project Structure

```
picoclaw/
├── cmd/picoclaw/              # CLI entry point (main.go, Cobra commands)
├── cmd/picoclaw-launcher-tui/ # Terminal UI launcher
├── pkg/
│   ├── agent/                 # Core agent loop, context builder, hooks, events
│   ├── providers/             # LLM provider integrations (OpenAI, Anthropic, etc.)
│   ├── channels/              # Chat platform integrations (15+ platforms)
│   ├── tools/                 # Tool registry and implementations
│   ├── bus/                   # Message bus (inbound/outbound)
│   ├── config/                # Config loading, security, env resolution
│   ├── credential/            # Secret resolution (file://, enc://)
│   ├── cron/                  # In-process cron scheduler
│   ├── heartbeat/             # Periodic agent nudge system
│   ├── health/                # Health check endpoints
│   ├── routing/               # Agent routing and message dispatch
│   ├── memory/                # Conversation memory/persistence
│   ├── mcp/                   # Model Context Protocol support
│   ├── logger/                # Structured logging
│   └── ...                    # (voice, media, state, utils, etc.)
├── web/
│   ├── backend/               # Go web server (REST API + WebSocket)
│   └── frontend/              # React SPA
├── workspace/                 # Skills, memory, AGENT.md, SOUL.md
├── docker/                    # Docker configs (3 profiles: agent, gateway, launcher)
├── config/                    # Example configs
└── docs/                      # Documentation (20+ guides, 7+ languages)
```

### Key Architectural Patterns

- **Message Bus** (`pkg/bus/`) — decouples channels from the agent. All platforms funnel into `InboundMessage` → agent → `OutboundMessage`.
- **Registry Pattern** — used for tools (`ToolRegistry`), agents (`AgentRegistry`), and channels.
- **Factory Pattern** — LLM providers are created dynamically based on config with fallback chains.
- **Interface-based design** — providers, channels, and tools all implement clean interfaces enabling pluggability.

---

## The Agent Loop — Core Reasoning Engine

The agent loop is the brain of the system. It lives in `pkg/agent/loop.go` and implements a think-act cycle.

### Conceptual Model

An **agent** is not code that runs — it's a configuration bundle:

```
AgentInstance = {
    Name, ID            — identity
    Provider + Model    — which LLM to use ("gpt-4o", "claude-sonnet")
    Tools               — what it can do (shell, file_read, web_search, etc.)
    Sessions            — conversation history per chat
    ContextBuilder      — system prompt from AGENT.md, SOUL.md, skills, memory
    MaxIterations       — safety cap on loop iterations
    MaxTokens           — per-response token limit
}
```

The **loop** is a repeating cycle:

```
repeat:
    1. Send everything (system prompt + history + tool results) to the LLM
    2. LLM responds with EITHER:
       a) Text only       → done, send it to the user
       b) Tool call(s)    → execute them, go back to step 1
```

### Message Flow — End to End

```
Channel (Telegram/Discord/WebSocket/etc.)
  │
  ▼
BaseChannel.HandleMessage()         ← shared across all 15+ channels
  │  ├─ Validates sender (allow-list)
  │  ├─ Builds bus.InboundMessage
  │  └─ Auto-triggers: typing indicator, reaction, placeholder
  │
  ▼
bus.PublishInbound()                 ← pushes onto buffered channel (cap: 64)
  │
  ▼
AgentLoop.Run() event loop          ← select { case msg := <-bus.InboundChan() }
  │
  ▼
processMessage()
  │  ├─ Transcribes audio if present
  │  ├─ Resolves route: which agent handles this?
  │  ├─ Checks for special commands
  │  └─ Builds processOptions
  │
  ▼
runAgentLoop() → runTurn()          ← the core loop
  │
  ├─ Load session history + summary
  ├─ BuildMessages() (system prompt + history + user message)
  ├─ Check context budget, compress if needed
  ├─ Save user message to session
  │
  │  turnLoop:
  │  ├─ Inject any pending steering messages
  │  ├─ Hooks: BeforeLLM (can modify/abort)
  │  ├─ Call LLM (Provider.Chat)
  │  │   ├─ Fallback chain if multiple candidates
  │  │   ├─ Retry on timeout (exponential backoff)
  │  │   └─ Retry on context overflow (compress + retry)
  │  ├─ Hooks: AfterLLM (can modify/abort)
  │  ├─ No tool calls? → break (done)
  │  ├─ For each tool call:
  │  │   ├─ Hooks: BeforeTool, ApproveTool (can deny)
  │  │   ├─ Tools.ExecuteWithContext(name, args)
  │  │   ├─ Hooks: AfterTool (can modify)
  │  │   └─ Append tool result to messages
  │  └─ Loop back
  │
  ▼
PublishOutbound()                   ← response goes back on the bus
  │
  ▼
Channel sends response to user
```

### Key Code Locations

| What | File | Line |
|------|------|------|
| AgentLoop struct | `pkg/agent/loop.go` | 37 |
| processOptions struct | `pkg/agent/loop.go` | 74 |
| Run() event loop | `pkg/agent/loop.go` | 384 |
| processMessage() | `pkg/agent/loop.go` | 1255 |
| runAgentLoop() | `pkg/agent/loop.go` | 1467 |
| runTurn() | `pkg/agent/loop.go` | 1581 |
| turnLoop (the for loop) | `pkg/agent/loop.go` | 1690 |
| Exit condition (no tool calls) | `pkg/agent/loop.go` | 2126 |
| Tool execution | `pkg/agent/loop.go` | 2375 |
| AgentInstance struct | `pkg/agent/instance.go` | 23 |

---

## Tool System

Tools are how the agent interacts with the world. The LLM can only think and talk — tools are its hands.

### Tool Interface

Every tool implements (`pkg/tools/base.go:6`):

```go
type Tool interface {
    Name() string                                           // "read_file"
    Description() string                                    // "Read the contents of a file..."
    Parameters() map[string]any                             // JSON Schema for arguments
    Execute(ctx context.Context, args map[string]any) *ToolResult
}
```

`Name()`, `Description()`, and `Parameters()` are sent to the LLM as a JSON menu. The LLM never sees `Execute()` — it just requests a tool call by name and arguments. The agent loop does the lookup and execution.

### ToolResult — Two Audiences

```go
type ToolResult struct {
    ForLLM  string    // Goes back into messages for the next LLM call
    ForUser string    // Sent directly to the user's chat (optional)
    IsError bool      // Tells the LLM something went wrong
    Async   bool      // Tool is running in background
    Media   []string  // Media refs produced by this tool
}
```

### Available Tools

| Tool | File | Purpose |
|------|------|---------|
| `read_file` | `filesystem.go` | Read file contents with pagination |
| `write_file` | `filesystem.go` | Write/create files |
| `list_dir` | `filesystem.go` | List directory contents |
| `edit_file` | `edit.go` | Search-and-replace edits |
| `append_file` | `edit.go` | Append to files |
| `exec` | `shell.go` | Execute shell commands |
| `web_search` | `web.go` | Search the web (multiple backends) |
| `web_fetch` | `web.go` | Fetch and parse web pages |
| `spawn` | `spawn.go` | Spawn async sub-agent |
| `subagent` | `subagent.go` | Spawn sync sub-agent |
| `cron` | `cron.go` | Create/manage scheduled jobs |
| `message` | `message.go` | Send messages to channels |
| `send_file` | `send_file.go` | Send files to channels |
| `i2c` / `spi` | `i2c.go` / `spi.go` | Hardware control (embedded systems) |
| MCP tools | `mcp_tool.go` | Model Context Protocol tools |

### How a Tool Call Works

```
1. LLM sees:    {"name": "read_file", "description": "...", "parameters": {...}}
2. LLM says:    "Call read_file(path='README.md')"
3. Agent loop:  ToolRegistry.Get("read_file") → ReadFileTool   ← string lookup in a map
4. Execute:     ReadFileTool.Execute(ctx, {"path": "README.md"})  ← real Go code runs
5. Result:      ToolResult{ ForLLM: "# PicoClaw\n..." }
6. Append:      result added to messages array
7. Loop:        LLM called again with file contents in context
```

---

## Web Search — Provider Abstraction

The `web_search` tool uses a single interface with swappable backends.

### Interface

```go
type SearchProvider interface {
    Search(ctx context.Context, query string, count int) (string, error)
}
```

### Seven Implementations

| Provider | API Style | Timeout |
|----------|-----------|---------|
| **Brave** | REST API → returns titles, URLs, snippets | 10s |
| **Perplexity** | LLM call to "sonar" model with built-in web access | 30s |
| **Tavily** | AI-powered search API | 10s |
| **DuckDuckGo** | HTML scraping, no API key needed | 10s |
| **SearXNG** | Self-hosted meta-search | 10s |
| **GLM Search** | Zhipu AI search (Chinese) | 10s |
| **Baidu** | Baidu search API (Chinese) | 30s |

### Provider Priority

At startup, `NewWebSearchTool()` (`web.go:730`) picks one based on config with this priority:

```
Perplexity > Brave > SearXNG > Tavily > DuckDuckGo > Baidu > GLM
```

Only one provider is active at a time.

### API Key Rotation

Both Brave and Perplexity support multiple API keys via `APIKeyPool`. If a key hits rate limits (429) or auth errors (401/403), the next key is tried automatically.

---

## LLM Response Contract

Every LLM call returns a structured `LLMResponse` (`pkg/providers/protocoltypes/types.go:27`):

```go
type LLMResponse struct {
    Content          string      // Text response
    ToolCalls        []ToolCall  // Tool calls requested (may be empty)
    FinishReason     string      // "stop", "tool_calls", etc.
    Usage            *UsageInfo  // Token counts
    Reasoning        string      // Chain-of-thought (if supported)
    ReasoningContent string      // Extended reasoning
}
```

The loop decision is always:

| `Content` | `ToolCalls` | Meaning | Loop action |
|-----------|-------------|---------|-------------|
| empty | `[{name: "web_search", ...}]` | "I need tools first" | Execute tools, loop back |
| `"The answer is..."` | `[]` (empty) | "Here's my answer" | `break`, send to user |
| `"Let me search..."` | `[{name: "web_search", ...}]` | Both text and tool calls | Execute tools, loop back |

This format is not picoclaw-specific — it's the standard API format used by OpenAI, Anthropic, Google, etc. Each provider adapter normalizes its specific API response into this common struct.

---

## Multi-Agent System

PicoClaw supports multi-agent at two levels.

### Level 1: Named Agents with Routing

Multiple agents can be configured, each with their own model, workspace, tools, and personality:

```json
{
  "agents": {
    "list": [
      { "id": "main", "default": true, "model": {"primary": "gpt-4o"} },
      { "id": "worker", "model": {"primary": "gpt-4o-mini"} }
    ]
  }
}
```

The `AgentRegistry` (`pkg/agent/registry.go`) stores them. The `RouteResolver` (`pkg/routing/route.go`) uses a 7-level priority cascade to decide which agent handles each message:

1. Peer binding (specific user)
2. Parent peer binding
3. Guild/server binding
4. Team binding
5. Account binding
6. Channel wildcard
7. Default agent (fallback)

### Level 2: Dynamic Sub-Agent Spawning

Agents can spawn child agents mid-conversation via two tools:

**`spawn`** (async — fire and forget):
- LLM calls `spawn(task="research X")`
- Child runs in a goroutine
- Parent continues working
- Result delivered via `pendingResults` channel (if parent still running) or as a new inbound message (if parent finished)

**`subagent`** (sync — wait for answer):
- LLM calls `subagent(task="analyze this")`
- Parent blocks until child finishes
- Child's result returned as tool result

### Sub-Agent Characteristics

| Aspect | Parent Turn | Child Sub-Turn |
|--------|-------------|----------------|
| Session | Persistent (disk) | Ephemeral (in-memory, max 50 msgs) |
| History | Full conversation history | Empty (`NoHistory: true`) |
| Tools | Full registry | Cloned copy |
| Timeout | None | Default 5 minutes |
| Max depth | 0 | Up to 3 levels |
| Token budget | Own budget | Can inherit parent's (shared atomic counter) |

### Sub-Agent Uses the Same Loop

There is no separate loop for sub-agents. Both parent and child call the exact same `al.runTurn(ctx, ts)` function. The only difference is the `turnState` object passed in — which carries different configuration (depth, session type, parent reference, etc.).

### Spawn Permissions

Controlled via config allowlist:

```json
{
  "id": "main",
  "subagents": {
    "allow_agents": ["worker"],
    "model": {"primary": "gpt-4o-mini"}
  }
}
```

Hard abort cascades: parent abort → all children abort → all grandchildren abort.

### Async Result Delivery (Two Paths)

When a child finishes async:

**Path A — Parent still running**: Child result arrives on `pendingResults` channel. Parent's loop polls it (non-blocking `select`/`default`) at the top of each iteration. Result injected as `[SubTurn Result]` message.

**Path B — Parent already finished**: The async callback publishes the result as a new `InboundMessage` on the bus (with `SenderID: "async:spawn"`). The main `Run()` loop picks it up and starts a brand new turn, sending the response as a follow-up message in the chat.

---

## Sub-Agent Identity & Isolation

### Main Agent Identity

The main agent's system prompt is built by `ContextBuilder.BuildSystemPrompt()` (`pkg/agent/context.go:132`), assembling four layers:

1. `getIdentity()` — hardcoded core identity ("You are picoclaw...")
2. `LoadBootstrapFiles()` — reads `AGENT.md`, `SOUL.md`, `IDENTITY.md`, `USER.md` from workspace
3. `BuildSkillsSummary()` — lists available skills
4. `GetMemoryContext()` — reads `workspace/memory/MEMORY.md`

### Current Sub-Agent Behavior (Problem)

Sub-agents **inherit the parent's full identity**. At `subturn.go:345`:

```go
agent := *baseAgent  // shallow copy — shares parent's ContextBuilder
```

The child gets the same system prompt (same AGENT.md, SOUL.md, skills, memory). Differentiation comes only from the task description passed as the user message.

The `agent_id` parameter on `spawn` only resolves the target agent's **model** (`loop.go:334-339`) — not its workspace, AGENT.md, or tools.

Additionally, `SubTurnConfig.ActualSystemPrompt` and `processOptions.SystemPromptOverride` exist as fields but are **dead code** — set in `subturn.go:361` but never read by `BuildMessages()`. The spawn and subagent tools build hardcoded generic system prompts ("You are a spawned subagent labeled X") instead of resolving the target agent's actual identity.

### Planned Design: Shared Workspace + Per-Agent Identity Directory

Sub-agents will share the parent's workspace (memory, user context, skills) but have their own identity files in a dedicated directory structure within the workspace:

```
workspace/
├── AGENT.md              ← main/default agent identity
├── SOUL.md               ← main agent soul/personality
├── IDENTITY.md           ← main agent identity (legacy fallback for AGENT.md)
├── USER.md               ← shared across all agents (same human user)
├── memory/               ← shared across all agents
│   └── MEMORY.md
├── skills/               ← shared across all agents (filtered per agent via config)
└── agents/
    ├── researcher/
    │   ├── AGENT.md      ← researcher's identity + frontmatter
    │   └── SOUL.md       ← researcher's personality
    └── coder/
        ├── AGENT.md
        └── SOUL.md
```

### File Resolution Cascade

For a sub-agent with id `researcher`:

| File | Resolution Order | Fallback |
|------|-----------------|----------|
| **AGENT.md** | `agents/researcher/AGENT.md` → `workspace/AGENT.md` | Agent-specific overrides workspace default |
| **SOUL.md** | `agents/researcher/SOUL.md` → `workspace/SOUL.md` | Agent-specific overrides workspace default |
| **USER.md** | `workspace/USER.md` only | Always shared — never per-agent |
| **Memory** | `workspace/memory/` only | Always shared — agents are roles of the same assistant |
| **Skills** | `workspace/skills/` only | Always shared — filtered per agent via `AgentConfig.Skills` in config |

When an agent-specific file exists (e.g., `agents/researcher/SOUL.md`), it **fully replaces** the workspace-level file — no merging.

For the `main`/default agent (or single-agent setups), the existing behavior is unchanged — files are read from the workspace root. The `agents/` directory is only used when explicitly configured agents exist.

### Configuration

Sub-agents must be explicitly declared in `config.json` — no auto-discovery from the `agents/` directory:

```json
{
  "agents": {
    "list": [
      {
        "id": "main",
        "default": true,
        "model": {"primary": "claude-sonnet"}
      },
      {
        "id": "researcher",
        "model": {"primary": "gpt-4o"},
        "skills": ["web_search"]
      },
      {
        "id": "coder",
        "model": {"primary": "claude-sonnet"},
        "skills": ["edit_file", "exec"]
      }
    ]
  }
}
```

The `agents/{id}/` directory provides the identity files. The `config.json` entry declares the agent exists and configures model, skills filter, and subagent permissions.

### What Shared vs. What's Per-Agent

| Aspect | Shared | Per-Agent |
|--------|--------|-----------|
| **Memory** (`MEMORY.md`, daily notes) | ✅ | — |
| **User context** (`USER.md`) | ✅ | — |
| **Skills** (`skills/`) | ✅ (filtered via config) | — |
| **Agent identity** (`AGENT.md`) | — | ✅ (full replace) |
| **Soul/personality** (`SOUL.md`) | — | ✅ (full replace, falls back to workspace) |
| **Identity** (`IDENTITY.md`) | — | Not used for sub-agents (legacy, main agent only) |
| **Model** | — | ✅ (from config) |
| **Sessions** | — | ✅ (ephemeral for sub-agents) |
| **Tool registry** | — | ✅ (cloned from parent, filtered) |

### Design Rationale

This approach was chosen over two alternatives:

1. **Fully isolated workspaces** (each agent gets `workspace-{id}/` with its own memory, USER.md, skills) — rejected because sub-agents are roles of the same assistant talking to the same user. Siloing memory and user context creates unnecessary duplication and prevents knowledge sharing.

2. **Config-driven system prompts** (identity as a string in `config.json`, like ZeroClaw) — rejected because Halfmoon already has a rich file-based identity system (AGENT.md with YAML frontmatter, SOUL.md, IDENTITY.md). Putting identity in JSON is awkward for long, rich personality definitions and doesn't leverage existing infrastructure.

The chosen approach (shared workspace + per-agent identity directory) gives sub-agents distinct identities while sharing the context that should be shared (memory, user, skills). It extends the existing `ContextBuilder` and `loadAgentDefinition()` pipeline rather than introducing new identity mechanisms.

### Implementation Changes Required

| Component | File | Change |
|-----------|------|--------|
| `ContextBuilder` | `context.go` | Add agent ID field so `loadAgentDefinition()` checks `agents/{id}/` first |
| `loadAgentDefinition()` | `definition.go` | Add agent-specific path resolution before workspace root fallback |
| `spawnSubTurn()` | `subturn.go` | Use target agent's `AgentInstance` from registry (not shallow copy of parent) when `agent_id` specified |
| Dead code removal | `subturn.go`, `loop.go` | Remove `SubTurnConfig.ActualSystemPrompt`, `processOptions.SystemPromptOverride` |
| Spawn/subagent tools | `spawn.go`, `subagent.go` | Stop building hardcoded system prompts — let target agent's `ContextBuilder` handle identity |
| Cache invalidation | `context.go` | Track `agents/{id}/` files in mtime-based cache |

---

## Observability & Extension Points

### No OpenTelemetry Today

There are no OTEL SDK imports, no Prometheus client, no Jaeger/Datadog agents. Clean slate.

### What Exists

**1. Event System** (`pkg/agent/eventbus.go`, `events.go`) — 19 structured event types covering the full lifecycle:

| Event | Data |
|-------|------|
| `TurnStart` / `TurnEnd` | Channel, duration, iterations, status |
| `LLMRequest` / `LLMResponse` | Model, message count, tool count, tokens |
| `LLMRetry` | Attempt, reason, backoff |
| `ToolExecStart` / `ToolExecEnd` | Tool name, args, duration, error status |
| `SubTurnSpawn` / `SubTurnEnd` | Parent-child correlation via TurnID/ParentTurnID |
| `ContextCompress` | Messages dropped, remaining |
| `Error` | Stage + message |

Multi-subscriber broadcaster, non-blocking, buffered channels. Currently **emitted but not consumed externally** — just logged.

**2. Hook System** (`pkg/agent/hooks.go`) — four pluggable interfaces:

- `EventObserver` — async listener for all events
- `LLMInterceptor` — `BeforeLLM` / `AfterLLM`
- `ToolInterceptor` — `BeforeTool` / `AfterTool`
- `ToolApprover` — gate that can deny tool execution

Each hook can continue, modify, deny, or abort.

**3. Structured Logging** (`pkg/logger/`) — zerolog with component tags, structured fields, dual output (console + file).

**4. Health Checks** (`pkg/health/server.go`) — `GET /health`, `GET /ready`, `POST /reload`.

**5. HTTP Middleware** (`web/backend/middleware/`) — panic recovery, request logging (method, path, status, duration), IP allowlist.

### Best Integration Point for OTEL

Mount an `EventObserver` hook — zero changes to existing code:

```go
hookManager.Mount(NamedHook("otel", otelObserver))
```

Every turn, LLM call, tool execution, sub-turn, and error would flow into traces and metrics.

---

## Secrets & Environment Variables

Picoclaw uses a split-config design. `config.json` holds non-sensitive settings. Secrets live separately through three mechanisms.

### 1. `.security.yml` — Primary Secrets File

Lives next to `config.json` (typically `~/.picoclaw/.security.yml`):

```yaml
model_list:
  gpt-4o:0:
    api_keys:
      - "sk-abc123..."

channels:
  telegram:
    token: "123456:ABC..."

web:
  brave:
    api_keys:
      - "BSA-key1..."
      - "BSA-key2..."
  perplexity:
    api_keys:
      - "pplx-key1..."
```

Written with `0o600` permissions (owner-only).

### 2. Environment Variables — Override `.security.yml`

Every security field has an `env:` struct tag. Resolved via `env.Parse()` at startup.

| Env Var | Overrides |
|---------|-----------|
| `PICOCLAW_CHANNELS_TELEGRAM_TOKEN` | `channels.telegram.token` |
| `PICOCLAW_CHANNELS_DISCORD_TOKEN` | `channels.discord.token` |
| `PICOCLAW_TOOLS_WEB_PERPLEXITY_ENABLED` | `web.perplexity.enabled` |
| `PICOCLAW_TOOLS_WEB_BAIDU_API_KEY` | `web.baidu_search.api_key` |
| `PICOCLAW_VOICE_ELEVENLABS_API_KEY` | `voice.elevenlabs_api_key` |
| `PICOCLAW_HOME` | Base directory for all picoclaw data |
| `PICOCLAW_CONFIG` | Path to config.json |

### 3. `file://` References — Point to Secret Files

```yaml
web:
  brave:
    api_keys:
      - "file://brave_api_key.txt"
```

The `credential.Resolver` (`pkg/credential/credential.go:110`) supports three formats:

- `""` → empty (for OAuth, no key needed)
- `"file://name.key"` → reads content from `configDir/name.key` (sandboxed, symlink-safe)
- `"sk-abc123..."` → plaintext, used as-is

### Load Order

```
1. Load config.json           → non-sensitive settings
2. Load .security.yml         → API keys, tokens, secrets
3. env.Parse()                → environment variables override .security.yml
4. Resolve file:// refs       → read actual key content from files
5. Merge into Config          → security values injected into config structs
```

### Sensitive Data Filtering

A `SensitiveDataCache` builds a `strings.Replacer` from all known secret values. Used in the agent loop to scrub secrets from tool results before they reach the LLM context or the user.

---

## Heartbeat System

A periodic nudge that wakes the agent up proactively.

### How It Works

`pkg/heartbeat/service.go` runs a timer loop:

1. Wait 1 second (initial delay)
2. Read `HEARTBEAT.md` from workspace → build prompt with current timestamp
3. If no user-defined tasks in the file → skip silently
4. Call `agentLoop.ProcessHeartbeat(prompt, channel, chatID)`
5. Send response to last active channel
6. Sleep for configured interval (default 30 min, minimum 5 min)
7. Repeat

### Configuration

```go
type HeartbeatConfig struct {
    Enabled  bool `json:"enabled"  env:"PICOCLAW_HEARTBEAT_ENABLED"`
    Interval int  `json:"interval" env:"PICOCLAW_HEARTBEAT_INTERVAL"` // minutes, min 5
}
```

### Which Model?

**The default agent's model.** No special override. It goes through `ProcessHeartbeat()` → `runAgentLoop()` → `runTurn()` — the same loop, same LLM call, same tool execution. But with stripped-down options:

| Option | Value | Why |
|--------|-------|-----|
| `SessionKey` | `"heartbeat"` | Isolated from user chats |
| `NoHistory` | `true` | Stateless every time |
| `EnableSummary` | `false` | No summarization |
| `SendResponse` | `false` | Heartbeat service handles delivery |

### Where Responses Go

The heartbeat service tracks the last active channel via `RecordLastChannel()`. Responses are published to wherever the user last interacted (Telegram, Discord, etc.).

---

## Cron System

Scheduled jobs that the LLM (or user) can create. The LLM itself can schedule future tasks.

### Architecture — In-Process, Not System-Level

No OS crontab. No external scheduler. A single Go goroutine with a smart timer (`pkg/cron/service.go`):

```go
func (cs *CronService) runLoop(stopChan chan struct{}) {
    for {
        nextWake := cs.getNextWakeMS()   // scan all jobs, find earliest due time
        timer.Reset(delay)                // sleep until that time

        select {
        case <-stopChan:                  // process shutting down
            return
        case <-cs.wakeChan:               // new job added, recalculate
            continue
        case <-timer.C:                   // time's up
            cs.checkJobs()                // execute all due jobs
        }
    }
}
```

If the picoclaw process dies, all cron schedules stop. They resume on restart (jobs are persisted in `{workspace}/cron/jobs.json`).

### Schedule Types

| Type | Field | Behavior |
|------|-------|----------|
| `"at"` | `atMs: 1711324800000` | Fire once at timestamp, auto-deletes |
| `"every"` | `everyMs: 3600000` | Repeat at fixed interval |
| `"cron"` | `expr: "0 9 * * *"` | Standard cron expressions (via `gronx` library) |

### Three Execution Modes

**1. Direct delivery** (`deliver: true`):
Message sent straight to channel. No agent involved.

**2. Command execution** (`command` field set):
Shell command executed via `ExecTool`, output sent to channel.

**3. Agent processing** (`deliver: false`):
Message becomes input to the agent loop via `ProcessDirectWithChannel()`. Full turn with tool access, LLM calls, everything.

### Cron vs Heartbeat

| | Heartbeat | Cron (agent mode) |
|--|-----------|-------------------|
| Entry point | `ProcessHeartbeat()` | `ProcessDirectWithChannel()` |
| Session history | `NoHistory: true` (stateless) | **Yes** — `"cron-{jobID}"` (accumulates) |
| Summarization | Disabled | Enabled |
| Agent path | `runAgentLoop()` directly | Full `processMessage()` |

### LLM-Created Cron Jobs

The LLM can schedule itself:

```
User: "Every Friday, summarize my git commits for the week"

LLM → call cron(action="add", name="weekly summary", schedule="0 17 * * 5",
                  message="Analyze this week's git commits and summarize", deliver=false)
```

Every Friday at 5pm, the agent wakes up, gets that message as input, runs the full loop (reads git log, etc.), and sends the summary to the user's channel.

---

## Code Quality Summary

| Domain | Score | Notes |
|--------|-------|-------|
| Architecture | 9/10 | Clean separation, interface-based, pluggable |
| Security | 9/10 | No hardcoded secrets, credential encryption, sensitive data filtering |
| Code Quality | 9/10 | 283+ files with proper error handling, structured logging, consistent patterns |
| Performance | 9/10 | Pure Go, zero CGO, targets embedded hardware |
| CI/CD | 9/10 | 6 GitHub Actions workflows, GoReleaser, Docker multi-profile |
| **Overall** | **9/10** | |

**Issues found:** 0 critical, 0 high, 7 low-priority TODOs (all documented).

**Console statements in frontend:** 16 instances — all appropriate error/warning logs.

**No dangerous function usage** (eval, exec, Function constructor) found.
