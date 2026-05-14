# ds-go-tools-client

![Build](https://github.com/grasp-labs/ds-go-tools-client/actions/workflows/ci.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/grasp-labs/ds-go-tools-client)](https://goreportcard.com/report/github.com/grasp-labs/ds-go-tools-client)
[![codecov](https://codecov.io/gh/grasp-labs/ds-go-tools-client/branch/main/graph/badge.svg)](https://codecov.io/gh/grasp-labs/ds-go-tools-client)
[![GitHub release](https://img.shields.io/github/v/release/grasp-labs/ds-go-tools-client)](https://github.com/grasp-labs/ds-go-tools-client/releases)
![License](https://img.shields.io/github/license/grasp-labs/ds-go-tools-client?cacheSeconds=60)

The shared Go contract between **ds-tools** (the tool execution service) and
**ds-mcp** (the LLM gateway / MCP server).

This module is intentionally tiny. It contains:

| File                 | Purpose                                                          |
|----------------------|------------------------------------------------------------------|
| `types.go`           | Wire types: `ToolEntry`, `InvokeRequest`, `InvokeResult`, etc.   |
| `errors.go`          | `ErrorClass` enum, `*Error` type, sentinels, status mapping.     |
| `service.go`         | `Service` interface — what ds-tools implements.                  |
| `client.go`          | HTTP client — what ds-mcp consumes.                              |
| `client_stream.go`   | SSE consumer for streaming tool invocations.                     |

A contract change is a single PR here plus a `go.mod` bump on both sides.

The canonical contract description lives in `harness_design.md` §0; this
module is the executable form of that contract.

## Versioning

`v0.x.y` while we iterate. Once the surface stabilises we tag `v1.0.0`
and follow strict semver — any breaking change to a wire type or to a
public function signature requires a major bump.

Adding new optional JSON fields to existing types is *not* a breaking
change. The client deliberately tolerates unknown fields when decoding
server responses.

## For ds-mcp (the consumer)

```go
import toolsclient "github.com/grasp-labs/ds-go-tools-client"

c := toolsclient.New(
    "https://tools.example.com/tools/v1",
    toolsclient.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
    toolsclient.WithUserAgent("ds-mcp/" + buildinfo.Version),
)

// Sync job — pull the catalogue since the last seen version.
page, err := c.List(ctx, bearer, lastSeenVersion)
if err != nil {
    // err is always *toolsclient.Error from a successful round-trip; only
    // transport/build failures appear as other types.
    return fmt.Errorf("list tools: %w", err)
}
for _, entry := range page.Data {
    cache.Put(entry)
}

// Dispatcher — invoke a tool.
res, err := c.Invoke(ctx, bearer, toolID, toolsclient.InvokeRequest{
    ToolUseID: modelToolUseID,
    Input:     args,
    TimeoutMS: 8000,
})
if err != nil {
    var te *toolsclient.Error
    if errors.As(err, &te) && te.Class == toolsclient.ErrorClassTimeout {
        // tool timed out — surface to the model as a tool error
    }
    return err
}
```

### Streaming

```go
res, err := c.InvokeStream(ctx, bearer, toolID, req, toolsclient.StreamHandler{
    OnStart: func(s toolsclient.StartEvent) {
        log.Info("tool started", "at", s.StartedAt)
    },
    OnChunk: func(text string) {
        // forward to the model client immediately
        modelStream.Write(text)
    },
})
if err != nil {
    // a server-side error event surfaces here as *toolsclient.Error
    return err
}
// res is the terminal result (output, tokens, completed_at, …)
```

The terminal `result` and `error` frames are **not** delivered via
callbacks — they're returned from `InvokeStream`. This keeps the
"did the call succeed?" question on the return path, where Go callers
expect it.

### Error handling

Every error returned from the client is either a `*toolsclient.Error`
(server-side failure, parsed from JSON body or SSE `error` frame) or a
transport-level error wrapped into `*toolsclient.Error` with class
`upstream_unavailable` / `timeout` / `cancelled` (see
`mapTransportError`). You can branch on the class via sentinel matching:

```go
switch {
case errors.Is(err, toolsclient.ErrTimeout):
    // retry with longer budget
case errors.Is(err, toolsclient.ErrUnauthorised):
    // refresh token
case errors.Is(err, toolsclient.ErrUpstreamUnavailable):
    // circuit break
}
```

## For ds-tools (the implementer)

Implement the `Service` interface, then wire it behind Echo routes that
deserialise into the shared types.

```go
import toolsclient "github.com/grasp-labs/ds-go-tools-client"

type registryService struct {
    store     registry.Store
    dispatcher dispatcher.Runner
}

var _ toolsclient.Service = (*registryService)(nil)

func (s *registryService) List(ctx context.Context, sinceVersion string) (*toolsclient.ListResponse, error) {
    return s.store.List(ctx, sinceVersion)
}

func (s *registryService) Get(ctx context.Context, toolID string) (*toolsclient.ToolEntry, error) {
    return s.store.Get(ctx, toolID)
}

func (s *registryService) Invoke(
    ctx context.Context,
    toolID string,
    req toolsclient.InvokeRequest,
    emit toolsclient.InvokeEmitter,
) (*toolsclient.InvokeResult, error) {
    return s.dispatcher.Run(ctx, dispatcher.Job{
        ToolID:    toolID,
        ToolUseID: req.ToolUseID,
        Input:     req.Input,
        TimeoutMS: req.TimeoutMS,
    }, emit)
}
```

The HTTP handler is responsible for choosing JSON-vs-SSE based on the
`Accept` header and translating `*toolsclient.Error` (or other errors)
to the right HTTP status via `toolsclient.HTTPStatusFor`.

```go
func (h *Handler) invoke(c echo.Context) error {
    var req toolsclient.InvokeRequest
    if err := c.Bind(&req); err != nil {
        return respondError(c, &toolsclient.Error{
            Class:   toolsclient.ErrorClassValidation,
            Message: err.Error(),
        })
    }
    if strings.Contains(c.Request().Header.Get("Accept"), "text/event-stream") {
        return h.streamSSE(c, req)
    }
    return h.runJSON(c, req)
}

func respondError(c echo.Context, err *toolsclient.Error) error {
    return c.JSON(toolsclient.HTTPStatusFor(err.Class), err)
}
```

### SSE wire format

```
event: start
data: {"started_at":"2025-01-02T03:04:05Z"}

event: chunk
data: {"text":"hello"}

event: chunk
data: {"text":" world"}

event: result
data: {"tool_use_id":"tu-1","output":"hello world","tokens_out":2, ...}
```

On failure, the terminal frame is `event: error` with a JSON body
matching `toolsclient.Error`. The client parses this and returns it as
the call's error.

Unknown event names (e.g. a future `progress`) are silently ignored by
the client so the server can add new event types without a client bump.

## Local dev

```
go vet ./...
go test -race ./...
```

## Module path

```
github.com/grasp-labs/ds-go-tools-client
```

Package name: `toolsclient`.
