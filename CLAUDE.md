# CLAUDE.md — Halfmoon

Ultra-lightweight personal AI assistant in Go. Runs on $10 hardware, <10MB RAM. Pure Go (`CGO_ENABLED=0`).

## Commands

```bash
# Build
make build                    # Build halfmoon binary
make build-launcher           # Build web launcher (TUI)

# Test
make test                     # Run Go tests (excludes web/) + web tests
go test ./pkg/agent/...       # Test single package
go test -run TestName ./pkg/  # Run specific test

# Lint & Format
make lint                     # golangci-lint run
make fmt                      # golangci-lint fmt
make fix                      # Auto-fix lint issues
make vet                      # go vet

# Full check before PR
make check                    # deps + fmt + vet + test
```

## Architecture

```
cmd/halfmoon/           → CLI entry (Cobra). 10 subcommands.
pkg/agent/              → Core agent loop. THE most important package.
  loop.go               → Main reasoning loop (runTurn, turnLoop at line 1690)
  instance.go           → AgentInstance struct — model, tools, sessions, config
  context.go            → System prompt builder (AGENT.md + SOUL.md + skills + memory)
  hooks.go              → BeforeLLM/AfterLLM/BeforeTool/AfterTool interceptors
  eventbus.go           → 19 event types, multi-subscriber broadcaster
  subturn.go            → Sub-agent spawning (async/sync)
  turn.go               → Turn state management, parent-child hierarchy
pkg/providers/          → LLM provider adapters. Factory pattern + fallback chains.
pkg/channels/           → Chat platforms. 15+ channels, all funnel through BaseChannel.
  base.go               → HandleMessage() — shared entry for all channels
  manager.go            → Message routing, outbound delivery
pkg/tools/              → Tool registry. Interface: Name/Description/Parameters/Execute.
  base.go               → Tool interface + ToolResult (ForLLM, ForUser, IsError)
  registry.go           → Map-based lookup, clone support for sub-agents
  http_request.go       → HTTP request tool. Domain allowlist, auth profiles, SSRF protection.
pkg/bus/                → Message bus. Decouples channels from agent.
pkg/config/             → Config loading. JSON config + .security.yml + env vars + file:// refs
pkg/cron/               → In-process cron scheduler. Single goroutine + timer.
pkg/heartbeat/          → Periodic agent nudge from HEARTBEAT.md
pkg/routing/            → 7-level priority cascade for agent selection
web/backend/            → Go web server (REST API + WebSocket)
web/frontend/           → React 19 + Vite + TypeScript + Jotai + TanStack Router
```

## Core Patterns — Follow These

### The Agent Loop

The loop at `pkg/agent/loop.go:1690` is: call LLM → if tool calls, execute them and loop → if text only, break and respond. Everything flows through `runTurn()`. Sub-agents use the exact same function.

### Message Bus

All channels publish `InboundMessage` to the bus. The agent loop reads from the bus. Responses go via `OutboundMessage`. Never bypass the bus — it's the universal decoupling point.

### Tool Interface

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    Execute(ctx context.Context, args map[string]any) *ToolResult
}
```

`ToolResult.ForLLM` goes back to the LLM context. `ToolResult.ForUser` goes to the chat. Register tools in the `ToolRegistry`.

### Provider Interface

Every LLM provider normalizes to `LLMResponse{Content, ToolCalls, FinishReason, Usage}`. The agent loop doesn't know or care which provider is behind the call.

### Hook System

4 interfaces: `EventObserver`, `LLMInterceptor`, `ToolInterceptor`, `ToolApprover`. Hooks can continue, modify, deny, or abort. Use hooks for deterministic behavior, not CLAUDE.md instructions.

### Config Split

- `config.json` → non-sensitive settings
- `.security.yml` → API keys, tokens (YAML, `0o600` permissions)
- Environment variables → override `.security.yml` (`HALFMOON_*` prefix)
- `file://` references → point to key files on disk

## Go Conventions

### Error Handling

- Always check `if err != nil` immediately after the call
- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`
- Use `errors.Is()` and `errors.As()` for error inspection
- Never silently swallow errors — log or return them

### Concurrency

- Use `sync.Map` for concurrent map access (see `activeTurnStates` in loop.go)
- Use `atomic` types for counters and flags (see `running`, `turnSeq`)
- Use `sync.RWMutex` for protecting shared state
- Pass `context.Context` as first parameter for cancellation

### Naming

- Interfaces: single-method interfaces use `-er` suffix (`SearchProvider`, `AsyncExecutor`)
- Constructors: `NewXxx()` returns the concrete type
- Package-level functions: avoid stutter (`tools.NewRegistry`, not `tools.NewToolRegistry`)
- Follow existing patterns in each package

### Testing

- Tests live next to the code: `foo.go` → `foo_test.go`
- Use `testify` for assertions where already used
- Mock interfaces, not implementations
- Run `make check` before considering work complete

### Logging

- Use `logger.InfoCF`, `logger.WarnCF`, `logger.ErrorCF` with component tag and structured fields
- Component tag is first arg: `"agent"`, `"tools"`, `"cron"`, etc.
- Include relevant context in `map[string]any` fields
- Debug level for verbose/frequent events, Info for significant actions

## Don't

- Don't add CGO dependencies. The project must build with `CGO_ENABLED=0`
- Don't bypass the message bus for channel communication
- Don't store secrets in `config.json` — use `.security.yml` or env vars
- Don't add tools that execute code without proper workspace sandboxing (`validatePathWithAllowPaths`)
- Don't mutate shared tool registries — clone them for sub-agents (`Tools.Clone()`)
- Don't persist sub-agent session history — they use ephemeral in-memory sessions
- Don't add external dependencies without checking `go.mod` first. Prefer stdlib
- Don't skip the `make check` validation cycle before marking work complete
- Don't modify the event system contract — 19 event types are consumed by hooks and observers
- Don't add `console.log` to frontend code except in error/warning paths
