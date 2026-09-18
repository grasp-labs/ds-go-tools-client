// Package toolsclient is the shared contract between ds-tools (the tool
// execution service) and ds-mcp (the LLM gateway / MCP server).
//
// The package is intentionally tiny: it defines the wire types, the
// service interface ds-tools implements, and an HTTP client ds-mcp
// consumes. A contract change is a single PR here plus a go.mod bump on
// both sides.
//
// The canonical contract description lives in `harness_design.md` §0;
// this package is the executable form of that contract.
package toolsclient

import "time"

// Effect classifies what a tool does to the resource it addresses. It is
// the catalogue's authoritative answer to "does calling this change
// anything", which consumers need in order to gate dispatch without
// maintaining their own list of verb names.
//
// The criterion, so classification is not a judgement call: a tool is
// EffectWrite when it changes, removes, or actuates the resource it
// addresses, or creates new persistent resources *as its product*. It is
// EffectRead when it only observes — even if delivering what it observed
// produces a derived artifact.
//
// That last clause is load-bearing. A read whose result overflows the
// inline budget is persisted to DS File and returned via
// InvokeResult.ArtifactFileID; the dispatcher does this for any tool,
// including a plain resource read. Such an artifact is delivery of
// content the caller already asked for, not mutation of what was
// addressed, so it does not make the tool EffectWrite.
//
// The empty string is not a valid classification. Consumers MUST treat an
// unset Effect as EffectWrite: an unclassified tool is potentially
// side-effecting, and failing closed is what lets this field roll out
// without a breaking release.
type Effect string

const (
	EffectRead  Effect = "read"
	EffectWrite Effect = "write"
)

// ToolEntry is the catalogue record for a single tool. It is what
// ds-mcp's sync job pulls down and caches.
//
// SourceVersion is an opaque monotonic value owned by ds-tools. ds-mcp
// stores the highest SourceVersion it has seen and passes it back as
// `since_version` on the next List call. Do not parse or compare its
// shape; only compare for equality and "is non-empty".
//
// Descriptions is keyed by ISO 639-1 language code. "en" is required;
// other languages are optional.
//
// InputSchema is a decoded JSON Schema. Callers that need to validate
// inputs should re-marshal it and feed to their schema validator.
//
// ResourceKey names the InputSchema property that identifies what the
// call addresses — "path" for the resource verbs, but deliberately not
// assumed: websearch addresses no platform resource and leaves this
// empty, grep takes an optional "path" alongside its required "query",
// and generate_image requires "prompt" with "path" naming only a
// destination container. A consumer that hardcodes "path" mis-reads all
// three, which is why the catalogue states it per tool.
type ToolEntry struct {
	ToolID              string            `json:"tool_id"`
	Name                string            `json:"name"`
	Descriptions        map[string]string `json:"descriptions"`
	InputSchema         map[string]any    `json:"input_schema"`
	Effect              Effect            `json:"effect"`
	ResourceKey         string            `json:"resource_key,omitempty"`
	Tags                []string          `json:"tags"`
	Cost                int               `json:"cost"`
	SupportsStreaming   bool              `json:"supports_streaming"`
	MaxResultTokens     int               `json:"max_result_tokens"`
	SchemaTokenEstimate int               `json:"schema_token_estimate"`
	SourceVersion       string            `json:"source_version"`
	ModifiedAt          time.Time         `json:"modified_at"`
}

// ListResponse is the page returned from the List endpoint.
//
// NextPageToken is nil when there are no further pages. Callers should
// feed a non-nil NextPageToken back as `since_version` on the next call
// (the server treats them as interchangeable cursors).
type ListResponse struct {
	Data          []ToolEntry `json:"data"`
	NextPageToken *string     `json:"next_page_token"`
}

// InvokeRequest is the body of POST /tools/{tool_id}/invoke.
//
// ToolUseID is supplied by the caller (ds-mcp uses the model's
// tool_use_id). It is the idempotency key: invoking with the same
// ToolUseID twice must produce the same observable effect.
//
// Input is the validated argument object for the tool.
//
// TimeoutMS is a *server-side* budget hint. The client's context.Context
// deadline governs the wall-clock cap on the client side; they are
// independent. Set both. A zero TimeoutMS lets the server pick a default.
type InvokeRequest struct {
	ToolUseID string         `json:"tool_use_id"`
	Input     map[string]any `json:"input"`
	TimeoutMS int            `json:"timeout_ms"`
}

// InvokeResult is the terminal payload of a tool execution.
//
// Output is the assistant-visible string and is always text. A tool that
// produces a binary result (a generated image, a rendered document, a
// large export) stores the payload in DS File (POST /file/ on DS File API
// v2) under the caller's bearer — so DS File's native owner/viewer ACLs
// govern the artifact — and returns the DS File UUID via ArtifactFileID,
// with a short human/model-readable summary of what was produced in
// Output. A storage URI (S3, GCS, presigned, or otherwise) must never
// appear in the result; the tool's actual storage backend is its own
// implementation detail behind DS File.
//
// ArtifactFileID is empty when there is no artifact.
//
// Truncated indicates the dispatcher capped the output at
// ToolEntry.MaxResultTokens. A tool that knows its result will exceed
// MaxResultTokens may pre-emptively persist the full result to DS File,
// set ArtifactFileID, put a stable summary stub in Output, and set
// Truncated.
type InvokeResult struct {
	ToolUseID      string    `json:"tool_use_id"`
	Output         string    `json:"output"`
	ArtifactFileID string    `json:"artifact_file_id,omitempty"`
	TokensOut      int       `json:"tokens_out"`
	Truncated      bool      `json:"truncated"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
}

// StartEvent is the payload of the SSE `start` frame. It carries the
// server's view of when execution began, for client-side timing.
type StartEvent struct {
	StartedAt time.Time `json:"started_at"`
}

// ChunkEvent is the payload of the SSE `chunk` frame. Each chunk is a
// piece of progressive output; concatenating Text from every chunk in
// order yields the streamed body. The terminal `result` frame's Output
// is authoritative.
type ChunkEvent struct {
	Text string `json:"text"`
}
