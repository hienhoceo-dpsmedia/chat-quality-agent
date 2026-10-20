package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCustomRequests(t *testing.T) {
	for _, suffix := range []string{"/v1", "/v1/", "/v1/chat/completions", "/v1/chat/completions/"} {
		for _, key := range []string{"", "test-secret"} {
			t.Run(suffix+"/"+key, func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.URL.Path != "/v1/chat/completions" || r.URL.RawQuery != "route=a%2Fb" {
						t.Errorf("wrong target: %s", r.URL)
					}
					wantAuth := ""
					if key != "" {
						wantAuth = "Bearer " + key
					}
					if r.Header.Get("Authorization") != wantAuth {
						t.Error("wrong authorization")
					}
					if key == "" && len(r.Header.Values("Authorization")) != 0 {
						t.Error("keyless header must be absent")
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["model"] != "vendor/model:latest" || body["max_tokens"] != float64(42) || body["max_completion_tokens"] != nil {
						t.Errorf("wrong request body: %v", body)
					}
					if calls == 2 && !strings.Contains(body["messages"].([]any)[1].(map[string]any)["content"].(string), "sample transcript") {
						t.Error("missing batch transcript")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
				}))
				defer srv.Close()
				p, err := NewCustomProvider(key, " vendor/model:latest ", 42, " "+srv.URL+suffix+"?route=a%2Fb ")
				if err != nil {
					t.Fatal(err)
				}
				resp, err := p.AnalyzeChat(context.Background(), "system", "sample transcript")
				if err != nil {
					t.Fatal(err)
				}
				if resp.Provider != "custom" || resp.Model != "vendor/model:latest" {
					t.Fatalf("wrong response: %+v", resp)
				}
				_, err = p.AnalyzeChatBatch(context.Background(), "system", []BatchItem{{ConversationID: "1", Transcript: "sample transcript"}})
				if err != nil {
					t.Fatal(err)
				}
				if calls != 2 {
					t.Fatalf("calls = %d", calls)
				}
			})
		}
	}
}

func TestCustomRejectsInvalidConfiguration(t *testing.T) {
	for _, endpoint := range []string{"", "example.com/v1", "ftp://example.com", "https://", "https://user:pass@example.com", "https://example.com/#fragment", "https://example.com/#", "https://example.com/%zz"} {
		if _, err := NewCustomProvider("", "model", 16, endpoint); err == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
	if _, err := NewCustomProvider("", "  ", 16, "https://example.com/v1"); err == nil {
		t.Error("accepted empty model")
	}
}
