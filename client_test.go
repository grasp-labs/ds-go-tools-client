package toolsclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestList_OK(t *testing.T) {
	mod := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, http.MethodGet; got != want {
			t.Errorf("method = %q, want %q", got, want)
		}
		if got, want := r.URL.Path, "/tools/v1/tools"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("since_version"), "v42"; got != want {
			t.Errorf("since_version = %q, want %q", got, want)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok-1" {
			t.Errorf("Authorization = %q", got)
		}
		token := "next-cursor"
		_ = json.NewEncoder(w).Encode(ListResponse{
			Data: []ToolEntry{{
				ToolID:        "echo",
				Name:          "Echo",
				Descriptions:  map[string]string{"en": "Echoes input"},
				InputSchema:   map[string]any{"type": "object"},
				Tags:          []string{"demo"},
				Cost:          1,
				SourceVersion: "v42",
				ModifiedAt:    mod,
			}},
			NextPageToken: &token,
		})
	}))
	defer srv.Close()

	c := New(srv.URL + "/tools/v1")
	got, err := c.List(context.Background(), "tok-1", "v42")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Data) != 1 || got.Data[0].ToolID != "echo" {
		t.Fatalf("unexpected data: %+v", got)
	}
	if got.NextPageToken == nil || *got.NextPageToken != "next-cursor" {
		t.Fatalf("NextPageToken: %v", got.NextPageToken)
	}
	if !got.Data[0].ModifiedAt.Equal(mod) {
		t.Errorf("ModifiedAt = %v, want %v", got.Data[0].ModifiedAt, mod)
	}
}

func TestList_FirstSyncOmitsSinceVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"data":[],"next_page_token":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.List(context.Background(), "tok", ""); err != nil {
		t.Fatalf("List: %v", err)
	}
}

func TestGet_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/tools/echo"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		_, _ = w.Write([]byte(`{"tool_id":"echo","name":"Echo"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	got, err := c.Get(context.Background(), "tok", "echo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ToolID != "echo" {
		t.Errorf("ToolID = %q", got.ToolID)
	}
}

func TestGet_NotFoundReturnsTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"class":"not_found","message":"no such tool"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Get(context.Background(), "tok", "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	var te *Error
	if !errors.As(err, &te) {
		t.Fatalf("err type = %T", err)
	}
	if te.Class != ErrorClassNotFound {
		t.Errorf("Class = %q", te.Class)
	}
	if te.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus = %d", te.HTTPStatus)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false")
	}
}

func TestGet_EmptyToolIDIsClientValidation(t *testing.T) {
	c := New("http://unused")
	_, err := c.Get(context.Background(), "tok", "")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want validation, got %v", err)
	}
}

func TestInvoke_OK(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		if r.URL.Path != "/tools/echo/invoke" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got, want := r.Header.Get("Accept"), "application/json"; got != want {
			t.Errorf("Accept = %q, want %q", got, want)
		}
		var req InvokeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		if req.ToolUseID != "tu-1" {
			t.Errorf("tool_use_id = %q", req.ToolUseID)
		}
		_ = json.NewEncoder(w).Encode(InvokeResult{
			ToolUseID:   req.ToolUseID,
			Output:      "ok",
			TokensOut:   1,
			StartedAt:   now,
			CompletedAt: now.Add(time.Second),
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Invoke(context.Background(), "tok", "echo", InvokeRequest{
		ToolUseID: "tu-1",
		Input:     map[string]any{"x": 1},
		TimeoutMS: 5000,
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.Output != "ok" || res.ToolUseID != "tu-1" {
		t.Errorf("res = %+v", res)
	}
}

func TestInvoke_ValidationFromServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"class":"validation","message":"bad input"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Invoke(context.Background(), "tok", "echo", InvokeRequest{
		ToolUseID: "tu-1",
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want validation, got %v", err)
	}
}

func TestInvoke_ServerReturnsNonJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>upstream is down</html>")
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Invoke(context.Background(), "tok", "echo", InvokeRequest{
		ToolUseID: "tu-1",
	})
	var te *Error
	if !errors.As(err, &te) {
		t.Fatalf("err type = %T", err)
	}
	if te.Class != ErrorClassUpstreamUnavailable {
		t.Errorf("Class = %q", te.Class)
	}
	if !strings.Contains(te.Message, "upstream is down") {
		t.Errorf("Message = %q", te.Message)
	}
}

func TestInvoke_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Invoke(ctx, "tok", "echo", InvokeRequest{ToolUseID: "tu-1"})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("want cancelled, got %v", err)
	}
}

func TestInvoke_MissingToolUseID(t *testing.T) {
	c := New("http://unused")
	_, err := c.Invoke(context.Background(), "tok", "echo", InvokeRequest{})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want validation, got %v", err)
	}
}

func TestNew_DefaultsAndOptions(t *testing.T) {
	c := New("https://example.com/tools/v1/")
	if c.BaseURL() != "https://example.com/tools/v1" {
		t.Errorf("BaseURL = %q", c.BaseURL())
	}

	hc := &http.Client{Timeout: time.Second}
	c2 := New("x", WithHTTPClient(hc), WithUserAgent("ua-test"))
	if c2.httpClient != hc {
		t.Errorf("WithHTTPClient not applied")
	}
	if c2.userAgent != "ua-test" {
		t.Errorf("WithUserAgent not applied: %q", c2.userAgent)
	}
}

func TestError_InterfaceAndIs(t *testing.T) {
	e := &Error{Class: ErrorClassTimeout, Message: "boom"}
	if e.Error() != "timeout: boom" {
		t.Errorf("Error() = %q", e.Error())
	}
	if !errors.Is(e, ErrTimeout) {
		t.Errorf("errors.Is should match by class")
	}
	if errors.Is(e, ErrNotFound) {
		t.Errorf("errors.Is should not cross classes")
	}
	other := fmt.Errorf("not a tools error")
	if errors.Is(e, other) {
		t.Errorf("errors.Is should not match unrelated error")
	}
}

func TestHTTPStatusFor_Roundtrip(t *testing.T) {
	classes := []ErrorClass{
		ErrorClassValidation,
		ErrorClassUnauthorised,
		ErrorClassNotFound,
		ErrorClassTimeout,
		ErrorClassCancelled,
		ErrorClassUpstreamUnavailable,
		ErrorClassInternal,
	}
	for _, c := range classes {
		s := HTTPStatusFor(c)
		got := ErrorClassFromHTTPStatus(s)
		if got != c {
			t.Errorf("roundtrip %q -> %d -> %q", c, s, got)
		}
	}
}
