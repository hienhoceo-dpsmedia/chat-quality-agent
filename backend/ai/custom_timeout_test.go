package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type timeoutRoundTripper func(*http.Request) (*http.Response, error)

func (f timeoutRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCustomTimeoutDefaultsAndLongHTTPBudget(t *testing.T) {
	transport := http.DefaultTransport
	defer func() { http.DefaultTransport = transport }()
	var budget time.Duration
	http.DefaultTransport = timeoutRoundTripper(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("custom request has no deadline")
		}
		budget = time.Until(deadline)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}]}`))}, nil
	})
	for _, tc := range []struct {
		name    string
		timeout time.Duration
	}{{"default", 0}, {"long", 600 * time.Second}} {
		t.Run(tc.name, func(t *testing.T) {
			var p *OpenAICompatibleProvider
			var err error
			if tc.timeout == 0 {
				p, err = NewCustomProvider("", "typed/model:latest", 16, "https://example.invalid/v1")
			} else {
				p, err = NewCustomProviderWithTimeout("", "typed/model:latest", 16, "https://example.invalid/v1", tc.timeout)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.AnalyzeChat(context.Background(), "system", "synthetic"); err != nil {
				t.Fatal(err)
			}
			want := tc.timeout
			if want == 0 {
				want = 120 * time.Second
			}
			if budget < want-time.Second || budget > want {
				t.Fatalf("effective HTTP/context budget %v, want %v", budget, want)
			}
		})
	}
}

func TestCustomTimeoutCancelsHTTPAndRetryBackoff(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprint("retry=", retry), func(t *testing.T) {
			var calls atomic.Int32
			cancelled := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if retry {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
					return
				}
				select {
				case <-r.Context().Done():
					close(cancelled)
				case <-time.After(2 * time.Second):
					t.Error("HTTP request was not cancelled")
				}
			}))
			defer srv.Close()
			p, err := NewCustomProviderWithTimeout("", "model", 16, srv.URL+"/v1", 75*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			_, err = p.AnalyzeChatBatch(context.Background(), "system", []BatchItem{{ConversationID: "1", Transcript: "synthetic"}})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected deadline error, got %v", err)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("timeout did not bound the entire call: %v", elapsed)
			}
			if !retry {
				select {
				case <-cancelled:
				case <-time.After(time.Second):
					t.Fatal("server did not observe cancellation")
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d", calls.Load())
			}
		})
	}
}
