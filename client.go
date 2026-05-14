package toolsclient

import (
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

// DefaultUserAgent is sent on every request unless overridden via
// WithUserAgent.
const DefaultUserAgent = "ds-go-tools-client/1"

// Client is the HTTP client ds-mcp uses to talk to ds-tools. It is
// safe for concurrent use by multiple goroutines.
//
// Client does not hold authentication state. The caller passes a
// bearer token per call (ds-mcp threads the inbound user's JWT
// through). An empty bearer is forwarded as no Authorization header;
// the server is expected to reject it.
type Client struct {
	baseURL    string
	httpClient *http.Client
	userAgent  string
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithHTTPClient overrides the *http.Client used for non-streaming
// calls. Use it to set timeouts, transports, or instrumented round
// trippers. Streaming calls reuse this client too; if you set a
// short Timeout, streaming will be capped by it — pass a separate
// client without Timeout if that's a problem (the SSE call honours
// context cancellation regardless).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithUserAgent overrides the User-Agent header value.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// New returns a Client targeting baseURL. baseURL must include the
// version-prefixed path root, for example
// "https://tools.example.com/tools/v1".
//
// If no WithHTTPClient option is supplied, http.DefaultClient is used.
// Callers that care about timeouts should pass their own.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: http.DefaultClient,
		userAgent:  DefaultUserAgent,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// BaseURL returns the configured base URL with any trailing slash removed.
func (c *Client) BaseURL() string { return c.baseURL }

// List fetches a page of the catalogue. Pass sinceVersion="" for the
// first sync; afterwards, pass the previously returned NextPageToken
// (or the highest SourceVersion already cached).
func (c *Client) List(ctx context.Context, bearer, sinceVersion string) (*ListResponse, error) {
	u := c.baseURL + "/tools"
	if sinceVersion != "" {
		u += "?since_version=" + url.QueryEscape(sinceVersion)
	}

	req, err := c.newRequest(ctx, http.MethodGet, u, bearer, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	var out ListResponse
	if err := c.doJSON(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get fetches a single ToolEntry by id.
func (c *Client) Get(ctx context.Context, bearer, toolID string) (*ToolEntry, error) {
	if toolID == "" {
		return nil, &Error{Class: ErrorClassValidation, Message: "tool_id is required"}
	}
	u := c.baseURL + "/tools/" + url.PathEscape(toolID)

	req, err := c.newRequest(ctx, http.MethodGet, u, bearer, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	var out ToolEntry
	if err := c.doJSON(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Invoke runs a tool and returns a single InvokeResult. It always
// sends `Accept: application/json`; if the tool is streaming on the
// server, the server collects chunks and returns a single result.
//
// Use InvokeStream when you want to consume progressive chunks.
func (c *Client) Invoke(ctx context.Context, bearer, toolID string, body InvokeRequest) (*InvokeResult, error) {
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
	req.Header.Set("Accept", "application/json")

	var out InvokeResult
	if err := c.doJSON(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// newRequest builds an http.Request with the standard headers applied.
func (c *Client) newRequest(ctx context.Context, method, u, bearer string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req, nil
}

// doJSON performs a JSON request and decodes the 2xx body into out.
// Non-2xx responses are decoded into *Error.
func (c *Client) doJSON(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return mapTransportError(req.Context(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil
		}
		// NOTE: unknown fields are *permitted* — older clients must
		// keep working when the server adds non-breaking fields.
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		return nil
	}

	return parseHTTPError(resp)
}

// parseHTTPError reads the response body and returns an *Error. The
// caller is responsible for closing resp.Body.
func parseHTTPError(resp *http.Response) error {
	const maxErrBody = 1 << 20 // 1 MiB
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))

	e := &Error{HTTPStatus: resp.StatusCode}
	if len(body) > 0 {
		if jerr := json.Unmarshal(body, e); jerr == nil && e.Class != "" {
			e.HTTPStatus = resp.StatusCode
			return e
		}
	}

	e.Class = ErrorClassFromHTTPStatus(resp.StatusCode)
	if len(body) > 0 {
		e.Message = strings.TrimSpace(string(body))
	} else {
		e.Message = http.StatusText(resp.StatusCode)
	}
	return e
}

// mapTransportError converts a Go HTTP transport error into an *Error
// when the error reflects a known failure mode.
func mapTransportError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled):
		return &Error{Class: ErrorClassCancelled, Message: err.Error()}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Class: ErrorClassTimeout, Message: err.Error()}
	}
	// Best-effort: if the context is done but the error didn't surface
	// it (some transports wrap), prefer the context's reason.
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.Canceled) {
			return &Error{Class: ErrorClassCancelled, Message: err.Error()}
		}
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return &Error{Class: ErrorClassTimeout, Message: err.Error()}
		}
	}
	return &Error{Class: ErrorClassUpstreamUnavailable, Message: err.Error()}
}
