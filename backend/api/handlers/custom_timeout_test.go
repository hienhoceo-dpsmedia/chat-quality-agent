package handlers

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestCustomTimeoutResolution(t *testing.T) {
	saved := savedAISettings{Config: aiSettingsRequest{Provider: "custom", Model: "typed/model:latest", BaseURL: "https://example.com/v1", TimeoutSeconds: 240}}
	for _, seconds := range []int{0, 5, 75, 600, -1, 1, 4, 601} {
		draft := saved.Config
		draft.TimeoutSeconds = seconds
		got, err := resolveAISettings(&draft, saved, "", true)
		if seconds != 0 && (seconds < 5 || seconds > 600) {
			if err == nil || err.Error() != "invalid_timeout" {
				t.Errorf("seconds %d: %v", seconds, err)
			}
			continue
		}
		want := seconds
		if want == 0 {
			want = 120
		}
		if err != nil || got.TimeoutSeconds != want {
			t.Errorf("seconds %d: got %d, error %v", seconds, got.TimeoutSeconds, err)
		}
		if saved.Config.TimeoutSeconds != 240 {
			t.Fatal("draft changed saved timeout")
		}
		if aiTestTimeout(got) != time.Duration(want)*time.Second {
			t.Error("connection test timeout differs from resolved settings")
		}
	}
	got, err := resolveAISettings(nil, saved, "", true)
	if err != nil || got.TimeoutSeconds != 240 {
		t.Fatalf("saved timeout not used: %d, %v", got.TimeoutSeconds, err)
	}
	for _, raw := range []string{"", "0", "5", "600", "4", "601", "-1", "bad"} {
		stored := savedAISettings{Config: aiSettingsRequest{Provider: "custom", Model: "m", BaseURL: "https://example.com/v1"}, TimeoutRaw: raw}
		resolved, err := resolveAISettings(nil, stored, "", true)
		invalid := raw == "4" || raw == "601" || raw == "-1" || raw == "bad"
		if invalid {
			if err == nil || err.Error() != "invalid_timeout" {
				t.Errorf("saved timeout %q: %v", raw, err)
			}
			// A valid full draft must be able to repair corrupt saved settings.
			draft := stored.Config
			draft.TimeoutSeconds = 60
			if got, err := resolveAISettings(&draft, stored, "", true); err != nil || got.TimeoutSeconds != 60 {
				t.Errorf("repair %q: %v", raw, err)
			}
		} else if err != nil || resolved.TimeoutSeconds < 5 || resolved.TimeoutSeconds > 600 {
			t.Errorf("saved timeout %q: %d, %v", raw, resolved.TimeoutSeconds, err)
		}
	}
	for _, provider := range []string{"claude", "gemini", "openai", "xai"} {
		cfg := aiSettingsRequest{Provider: provider, APIKey: "test-only", TimeoutSeconds: 601}
		resolved, err := resolveAISettings(&cfg, saved, "", true)
		if err != nil || aiTestTimeout(resolved) != 30*time.Second {
			t.Errorf("builtin %s timeout changed: %v", provider, err)
		}
	}
	if _, err := decodeAIDraft(strings.NewReader(`{"provider":"custom","timeout_seconds":"not-a-number"}`)); err == nil {
		t.Error("accepted nonnumeric timeout")
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	aiConfigError(c, errors.New("invalid_timeout"))
	if !strings.Contains(w.Body.String(), `"error":"invalid_timeout"`) || !strings.Contains(w.Body.String(), "600") {
		t.Fatalf("missing Vietnamese timeout validation: %s", w.Body.String())
	}
}
