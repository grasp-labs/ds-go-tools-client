package toolsclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeSSE(w http.ResponseWriter, event, data string) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestInvokeStream_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Accept"), "text/event-stream"; got != want {
			t.Errorf("Accept = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(w, "start", `{"started_at":"2025-01-02T03:04:05Z"}`)
		writeSSE(w, "chunk", `{"text":"hello"}`)
		writeSSE(w, "chunk", `{"text":" world"}`)
		writeSSE(w, "result", `{"tool_use_id":"tu-1","output":"hello world","tokens_out":2,"started_at":"2025-01-02T03:04:05Z","completed_at":"2025-01-02T03:04:06Z"}`)
	}))
	defer srv.Close()

	var (
		mu      sync.Mutex
		chunks  []string
		starts  []StartEvent
	)
	c := New(srv.URL)
	res, err := c.InvokeStream(context.Background(), "tok", "echo", InvokeRequest{
		ToolUseID: "tu-1",
	}, StreamHandler{
		OnStart: func(s StartEvent) {
			mu.Lock()
			starts = append(starts, s)
			mu.Unlock()
		},
		OnChunk: func(s string) {
			mu.Lock()
			chunks = append(chunks, s)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	if res.Output != "hello world" {
		t.Errorf("Output = %q", res.Output)
	}
	if len(starts) != 1 {
		t.Errorf("starts = %d, want 1", len(starts))
	}
	if strings.Join(chunks, "") != "hello world" {
		t.Errorf("chunks joined = %q", strings.Join(chunks, ""))
	}
}

func TestInvokeStream_ErrorFrame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(w, "start", `{"started_at":"2025-01-02T03:04:05Z"}`)
		writeSSE(w, "chunk", `{"text":"partial"}`)
		writeSSE(w, "error", `{"class":"timeout","message":"tool deadline"}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.InvokeStream(context.Background(), "tok", "echo", InvokeRequest{
		ToolUseID: "tu-1",
	}, StreamHandler{})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want timeout, got %v", err)
	}
}

func TestInvokeStream_NoTerminalEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(w, "chunk", `{"text":"x"}`)
		// Connection closes here.
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.InvokeStream(context.Background(), "tok", "echo", InvokeRequest{
		ToolUseID: "tu-1",
	}, StreamHandler{})
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("want upstream_unavailable, got %v", err)
	}
}

func TestInvokeStream_HTTPErrorBeforeSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"class":"unauthorised","message":"no token"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.InvokeStream(context.Background(), "tok", "echo", InvokeRequest{
		ToolUseID: "tu-1",
	}, StreamHandler{})
	if !errors.Is(err, ErrUnauthorised) {
		t.Fatalf("want unauthorised, got %v", err)
	}
}

func TestInvokeStream_MultilineData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Multi-line data: two `data:` lines joined with "\n" inside the
		// JSON payload.
		_, _ = fmt.Fprint(w, "event: chunk\ndata: {\"text\":\"line-a\\nline-b\"}\n\n")
		writeSSE(w, "result", `{"tool_use_id":"tu-1","output":"line-a\nline-b","tokens_out":1,"started_at":"2025-01-02T03:04:05Z","completed_at":"2025-01-02T03:04:06Z"}`)
	}))
	defer srv.Close()

	var got string
	c := New(srv.URL)
	res, err := c.InvokeStream(context.Background(), "tok", "echo", InvokeRequest{ToolUseID: "tu-1"}, StreamHandler{
		OnChunk: func(s string) { got = s },
	})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	if got != "line-a\nline-b" {
		t.Errorf("chunk = %q", got)
	}
	if res.Output != "line-a\nline-b" {
		t.Errorf("result output = %q", res.Output)
	}
}

func TestInvokeStream_UnknownEventTypeIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(w, "progress", `{"percent":50}`)
		writeSSE(w, "result", `{"tool_use_id":"tu-1","output":"done","tokens_out":1,"started_at":"2025-01-02T03:04:05Z","completed_at":"2025-01-02T03:04:06Z"}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.InvokeStream(context.Background(), "tok", "echo", InvokeRequest{ToolUseID: "tu-1"}, StreamHandler{})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	if res.Output != "done" {
		t.Errorf("Output = %q", res.Output)
	}
}

func TestInvokeStream_ContextCancelMidStream(t *testing.T) {
	releaseHandler := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(w, "chunk", `{"text":"x"}`)
		<-r.Context().Done()
		close(releaseHandler)
	}))
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())

	chunkReceived := make(chan struct{}, 1)
	go func() {
		// Cancel once we know we've actually started streaming.
		<-chunkReceived
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := c.InvokeStream(ctx, "tok", "echo", InvokeRequest{ToolUseID: "tu-1"}, StreamHandler{
		OnChunk: func(string) {
			select {
			case chunkReceived <- struct{}{}:
			default:
			}
		},
	})
	if !errors.Is(err, ErrCancelled) && !errors.Is(err, ErrUpstreamUnavailable) {
		// Either is acceptable: a fast cancel may surface as the read
		// failing (upstream_unavailable on the transport) or as a
		// context.Canceled (mapped to cancelled).
		t.Fatalf("want cancelled or upstream_unavailable, got %v", err)
	}
	<-releaseHandler
}
