package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCreateJobAcceptsAllAIProviders(t *testing.T) {
	for _, provider := range []string{"claude", "gemini", "openai", "xai", "custom"} {
		req := CreateJobRequest{Name: "Test", JobType: "qc_analysis", InputChannelIDs: []string{"channel"}, AIProvider: provider, Outputs: []byte(`[]`), OutputSchedule: "none", ScheduleType: "manual"}
		if err := binding.Validator.ValidateStruct(req); err != nil {
			t.Errorf("%s rejected: %v", provider, err)
		}
	}
}

func TestResolveAICredentialsAndDraftIsolation(t *testing.T) {
	const encryptionKey = "12345678901234567890123456789012"
	encrypted, err := pkg.Encrypt([]byte("saved-secret"), encryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	saved := savedAISettings{Config: aiSettingsRequest{Provider: "custom", Model: "saved-model", BaseURL: "https://example.com/v1"}, EncryptedKey: encrypted}
	before := saved
	for _, tc := range []struct {
		name                          string
		draft                         *aiSettingsRequest
		wantKey, wantModel, wantError string
	}{
		{"saved", nil, "saved-secret", "saved-model", ""},
		{"same endpoint normalized", &aiSettingsRequest{Provider: "custom", Model: " draft-model ", BaseURL: " https://example.com/v1/chat/completions/ "}, "saved-secret", "draft-model", ""},
		{"clear", &aiSettingsRequest{Provider: "custom", Model: "draft", BaseURL: "https://example.com/v1", ClearAPIKey: true}, "", "draft", ""},
		{"new endpoint requires intent", &aiSettingsRequest{Provider: "custom", Model: "draft", BaseURL: "https://other.example/v1"}, "", "", "api_key_required"},
		{"new endpoint clear", &aiSettingsRequest{Provider: "custom", Model: "draft", BaseURL: "https://other.example/v1", ClearAPIKey: true}, "", "draft", ""},
		{"new key", &aiSettingsRequest{Provider: "custom", Model: "draft", BaseURL: "https://other.example/v1", APIKey: " new-secret "}, "new-secret", "draft", ""},
		{"provider switch", &aiSettingsRequest{Provider: "openai", BaseURL: "https://example.com/v1"}, "", "", "api_key_required"},
		{"builtin clear forbidden", &aiSettingsRequest{Provider: "openai", ClearAPIKey: true}, "", "", "api_key_required"},
		{"invalid endpoint", &aiSettingsRequest{Provider: "custom", Model: "m", BaseURL: "invalid", APIKey: "k"}, "", "", "invalid_base_url"},
		{"missing model", &aiSettingsRequest{Provider: "custom", BaseURL: "https://example.com/v1"}, "", "", "model_required"},
		{"unsupported", &aiSettingsRequest{Provider: "unknown"}, "", "", "unsupported_provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveAISettings(tc.draft, saved, encryptionKey, true)
			if tc.wantError != "" {
				if err == nil || err.Error() != tc.wantError {
					t.Fatalf("got %v, want %s", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.APIKey != tc.wantKey || got.Model != tc.wantModel {
				t.Error("incorrect resolved draft")
			}
			if !reflect.DeepEqual(saved, before) {
				t.Fatal("saved config mutated")
			}
		})
	}
}

func TestResolveAIKeylessAndDiscovery(t *testing.T) {
	saved := savedAISettings{Config: aiSettingsRequest{Provider: "claude"}}
	draft := aiSettingsRequest{Provider: "custom", BaseURL: "https://example.com/v1", ClearAPIKey: true}
	if _, err := resolveAISettings(&draft, saved, "", false); err != nil {
		t.Fatal(err)
	}
	draft.Model = "typed/model:version"
	if _, err := resolveAISettings(&draft, saved, "", true); err != nil {
		t.Fatal(err)
	}
	// A brand-new keyless config needs no previous key or explicit clear.
	draft.ClearAPIKey = false
	if _, err := resolveAISettings(&draft, saved, "", true); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"claude", "gemini", "openai", "xai"} {
		if _, err := resolveAISettings(&aiSettingsRequest{Provider: provider}, saved, "", true); err == nil {
			t.Errorf("accepted %s without key", provider)
		}
	}
	if got := staticModels("custom"); len(got) != 0 {
		t.Error("custom must not get static builtin models")
	}
}

func TestChangedKeylessEndpointRequiresExplicitIntent(t *testing.T) {
	saved := savedAISettings{Config: aiSettingsRequest{Provider: "custom", Model: "model", BaseURL: "https://old.example/v1"}}
	draft := aiSettingsRequest{Provider: "custom", Model: "model", BaseURL: "https://new.example/v1"}
	if _, err := resolveAISettings(&draft, saved, "", true); err == nil || err.Error() != "api_key_required" {
		t.Fatalf("changed endpoint needs explicit key/clear: %v", err)
	}
	draft.ClearAPIKey = true
	if _, err := resolveAISettings(&draft, saved, "", true); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalAIDraftBody(t *testing.T) {
	for _, body := range []string{"", "  "} {
		got, err := decodeAIDraft(strings.NewReader(body))
		if err != nil || got != nil {
			t.Fatalf("empty body: %v", err)
		}
	}
	got, err := decodeAIDraft(strings.NewReader(`{"provider":"custom","model":"draft","clear_api_key":true}`))
	if err != nil || got == nil || got.Provider != "custom" || !got.ClearAPIKey {
		t.Fatalf("draft: %v", err)
	}
	for _, body := range []string{"null", "{", `{} {}`} {
		if _, err := decodeAIDraft(strings.NewReader(body)); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
}

func TestModelCacheIdentity(t *testing.T) {
	cfg := aiSettingsRequest{Provider: "custom", BaseURL: "https://example.com/v1/chat/completions", APIKey: "a", Model: "m"}
	base := modelCacheIdentity(cfg)
	for _, field := range []string{"provider", "endpoint", "key"} {
		changed := cfg
		switch field {
		case "provider":
			changed.Provider = "openai"
		case "endpoint":
			changed.BaseURL += "?route=other"
		case "key":
			changed.APIKey = "b"
		}
		if modelCacheIdentity(changed) == base {
			t.Errorf("cache ignores %s", field)
		}
	}
	cfg.Model = "other"
	cfg.TimeoutSeconds = 600
	if modelCacheIdentity(cfg) != base {
		t.Error("model selection should not invalidate catalog")
	}
}

func TestCustomAIHTTPDraftAndSavedModes(t *testing.T) {
	transport := http.DefaultTransport
	defer func() { http.DefaultTransport = transport }()
	var budgets []time.Duration
	http.DefaultTransport = handlerTimeoutTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("request missing deadline")
		}
		budgets = append(budgets, time.Until(deadline))
		return transport.RoundTrip(r)
	})
	t.Setenv("ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Setenv("JWT_SECRET", "12345678901234567890123456789012")
	t.Setenv("DB_PASSWORD", "test-only")
	var requestedModels []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Authorization")) != 0 {
			t.Error("unexpected credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/saved/chat/completions", "/draft/chat/completions":
			var body struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			requestedModels = append(requestedModels, body.Model)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
		case "/draft/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"vendor/model:latest"}]}`))
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	saved := []models.AppSetting{
		{SettingKey: "ai_provider", ValuePlain: "custom"},
		{SettingKey: "ai_model", ValuePlain: "saved-model"},
		{SettingKey: "ai_base_url", ValuePlain: srv.URL + "/saved"},
		{SettingKey: "ai_timeout_seconds", ValuePlain: "240"},
	}
	store, err := gorm.Open(mysql.New(mysql.Config{DSN: "unused:unused@tcp(127.0.0.1:1)/unused", SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := store.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := store.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
		rows, ok := tx.Statement.Dest.(*[]models.AppSetting)
		if !ok {
			t.Error("unexpected settings read")
			tx.AddError(gorm.ErrInvalidData)
			return
		}
		*rows = append([]models.AppSetting(nil), saved...)
		tx.RowsAffected = int64(len(saved))
	}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = store
	defer func() { db.DB = previous }()
	router := gin.New()
	router.POST("/test", TestAIKey)
	router.POST("/refresh", RefreshAIModels)
	router.PUT("/save", SaveAISettings)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		return w
	}
	draft := `{"provider":"custom","model":"draft-model","base_url":"` + srv.URL + `/draft/chat/completions/","clear_api_key":true,"timeout_seconds":75}`
	if w := request(http.MethodPost, "/test", draft); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"model":"draft-model"`) {
		t.Fatalf("draft test: %d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodPost, "/test", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"model":"saved-model"`) {
		t.Fatalf("saved test: %d %s", w.Code, w.Body.String())
	}
	discovery := strings.Replace(draft, `"model":"draft-model"`, `"model":""`, 1)
	if w := request(http.MethodPost, "/refresh", discovery); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "vendor/model:latest") {
		t.Fatalf("draft discovery: %d %s", w.Code, w.Body.String())
	}
	// These must fail validation before any database write/transaction. The
	// intentionally unconnected store would surface accidental writes as 500s.
	for _, body := range []string{`{"provider":"custom","model":"m","base_url":"invalid"}`, `{"provider":"custom","base_url":"https://example.com/v1"}`, `{"provider":"openai"}`} {
		if w := request(http.MethodPut, "/save", body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid save: %d %s", w.Code, w.Body.String())
		}
	}
	if !reflect.DeepEqual(requestedModels, []string{"draft-model", "saved-model"}) {
		t.Fatalf("draft/saved requests mixed: %v", requestedModels)
	}
	if len(budgets) != 3 {
		t.Fatalf("unexpected outbound calls: %d", len(budgets))
	}
	for i, want := range []time.Duration{75 * time.Second, 240 * time.Second, 20 * time.Second} {
		if budgets[i] < want-time.Second || budgets[i] > want {
			t.Errorf("request %d budget %v, want %v", i, budgets[i], want)
		}
	}
	for _, path := range []string{"/save", "/test"} {
		method := http.MethodPost
		if path == "/save" {
			method = http.MethodPut
		}
		w := request(method, path, strings.Replace(draft, `"timeout_seconds":75`, `"timeout_seconds":601`, 1))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error":"invalid_timeout"`) {
			t.Fatalf("invalid timeout before writes/requests: %d %s", w.Code, w.Body.String())
		}
	}
}

type handlerTimeoutTransport func(*http.Request) (*http.Response, error)

func (f handlerTimeoutTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
