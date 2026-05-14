package toolsclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// StreamHandler holds optional callbacks invoked while a streaming
// invocation is in progress. Both fields are optional; nil callbacks
// are skipped.
//
// OnStart fires once when the server emits the `start` event.
//
// OnChunk fires for each `chunk` event. The chunk text is exactly
// what the server emitted; concatenating chunks in order yields the
// streamed body.
//
// The terminal `result` and `error` frames are *not* delivered via
// callbacks — they are returned from InvokeStream as (*InvokeResult,
// nil) and (nil, *Error) respectively. This keeps the "did the call
// succeed?" question on the return path, where Go callers expect it.
type StreamHandler struct {
	OnStart func(StartEvent)
	OnChunk func(string)
}

// InvokeStream runs a tool and consumes its SSE response. It blocks
// until the server emits a `result` or `error` event, then returns.
//
// If the server emits an `error` event, InvokeStream returns
// (nil, *Error). If the connection ends before a terminal event,
// InvokeStream returns (nil, *Error) with ErrorClassUpstreamUnavailable.
//
// Callbacks run on the goroutine that called InvokeStream. They must
// not block indefinitely; if you need to hand chunks to another
// goroutine, push them onto a buffered channel inside the callback.
func (c *Client) InvokeStream(ctx context.Context, bearer, toolID string, body InvokeRequest, h StreamHandler) (*InvokeResult, error) {
	if toolID == "" {
		return nil, &Error{Class: ErrorClassValidation, Message: "tool_id is required"}
	}
	if body.ToolUseID == "" {
		return nil, &Error{Class: ErrorClassValidation, Message: "tool_use_id is required"}
	}

	u := c.baseURL + "/tools/" + url.PathEscape(toolID) + "/invoke"
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal invoke request: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPost, u, bearer, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, mapTransportError(ctx, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseHTTPError(resp)
	}

	return parseSSE(ctx, resp.Body, h)
}

// parseSSE implements just enough of the EventSource spec for our
// contract: line-buffered, `event:` and `data:` fields, blank line
// terminates an event. Multi-line `data:` is joined with "\n".
// `id:` and `retry:` are accepted but ignored. Comment lines (":") are
// dropped.
func parseSSE(ctx context.Context, r io.Reader, h StreamHandler) (*InvokeResult, error) {
	reader := bufio.NewReader(r)

	var (
		eventName string
		dataLines []string
	)

	dispatch := func() (*InvokeResult, bool, error) {
		defer func() {
			eventName = ""
			dataLines = nil
		}()
		if eventName == "" && len(dataLines) == 0 {
			return nil, false, nil
		}
		data := []byte(strings.Join(dataLines, "\n"))

		switch eventName {
		case "start":
			if h.OnStart != nil {
				var se StartEvent
				if jerr := json.Unmarshal(data, &se); jerr == nil {
					h.OnStart(se)
				}
			}
		case "chunk":
			if h.OnChunk != nil {
				var ce ChunkEvent
				if jerr := json.Unmarshal(data, &ce); jerr == nil {
					h.OnChunk(ce.Text)
				}
			}
		case "result":
			var ir InvokeResult
			if jerr := json.Unmarshal(data, &ir); jerr != nil {
				return nil, true, &Error{
					Class:   ErrorClassInternal,
					Message: fmt.Sprintf("decode result event: %v", jerr),
				}
			}
			return &ir, true, nil
		case "error":
			ee := &Error{}
			if jerr := json.Unmarshal(data, ee); jerr != nil || ee.Class == "" {
				return nil, true, &Error{
					Class:   ErrorClassInternal,
					Message: fmt.Sprintf("decode error event: %s", string(data)),
				}
			}
			return nil, true, ee
		default:
			// Unknown event types are silently ignored to leave room
			// for forward-compatible additions (e.g. "progress").
		}
		return nil, false, nil
	}

	for {
		line, err := reader.ReadString('\n')

		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			switch {
			case trimmed == "":
				if ir, done, derr := dispatch(); done {
					return ir, derr
				}
			case strings.HasPrefix(trimmed, ":"):
				// comment / heartbeat — ignore
			default:
				field, rest, ok := splitField(trimmed)
				if !ok {
					// SSE allows lines without a colon to be treated
					// as field names with empty values; we don't use
					// any such fields, so ignore.
					break
				}
				switch field {
				case "event":
					eventName = rest
				case "data":
					dataLines = append(dataLines, rest)
				case "id", "retry":
					// not used
				}
			}
		}

		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, mapTransportError(ctx, ctxErr)
			}
			if errors.Is(err, io.EOF) {
				return nil, &Error{
					Class:   ErrorClassUpstreamUnavailable,
					Message: "stream ended before terminal event",
				}
			}
			return nil, mapTransportError(ctx, err)
		}
	}
}

// splitField parses one SSE line into (field, value). Per the spec a
// single leading space after the colon is stripped.
func splitField(line string) (field, value string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return "", "", false
	}
	field = line[:idx]
	value = line[idx+1:]
	value = strings.TrimPrefix(value, " ")
	return field, value, true
}
