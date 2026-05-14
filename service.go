package toolsclient

import "context"

// Service is the contract ds-tools implements. ds-tools wires this
// behind the HTTP routes; ds-mcp does not consume this interface
// directly (it uses Client). Keeping it in this package makes the
// "implements" relationship checkable at compile time on the ds-tools
// side via `var _ toolsclient.Service = (*MyImpl)(nil)`.
//
// All methods take a context.Context and must honour cancellation.
//
// List paginates by `sinceVersion`. The first sync passes "" and reads
// the whole catalogue; subsequent syncs pass the highest SourceVersion
// already cached, or the previously returned NextPageToken (they are
// interchangeable cursors).
//
// Invoke runs a single tool. For non-streaming tools, the
// implementation returns the *InvokeResult and never calls emit. For
// streaming tools, the implementation may call emit zero or more
// times with progress chunks and must return the final *InvokeResult
// when execution completes.
//
// The InvokeEmitter is supplied by the HTTP layer. When the client
// requested SSE, emit writes a `chunk` frame; when the client requested
// JSON, emit buffers (or no-ops, depending on the policy implemented in
// the handler). The Service implementation does not need to know which.
type Service interface {
	List(ctx context.Context, sinceVersion string) (*ListResponse, error)
	Get(ctx context.Context, toolID string) (*ToolEntry, error)
	Invoke(ctx context.Context, toolID string, req InvokeRequest, emit InvokeEmitter) (*InvokeResult, error)
}

// InvokeEmitter is the callback Service implementations use to push
// progress chunks during a streaming invocation. Returning an error
// from emit indicates the consumer has gone away (e.g. client
// disconnect); the implementation should stop work and propagate it.
type InvokeEmitter func(text string) error
