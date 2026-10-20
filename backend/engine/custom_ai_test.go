package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type customTimeoutTransport func(*http.Request) (*http.Response, error)

func (f customTimeoutTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAnalyzerCustomSavedConfiguration(t *testing.T) {
	transport := http.DefaultTransport
	defer func() { http.DefaultTransport = transport }()
	var budget time.Duration
	http.DefaultTransport = customTimeoutTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("analyzer custom request missing deadline")
		}
		budget = time.Until(deadline)
		return transport.RoundTrip(r)
	})
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected target: %s", r.URL)
		}
		if len(r.Header.Values("Authorization")) != 0 {
			t.Error("keyless analyzer sent authorization")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer srv.Close()
	// Replace only GORM's settings-read callback: no database service or extra
	// test dependency is needed, while exercising getProvider itself end to end.
	store, err := gorm.Open(mysql.New(mysql.Config{DSN: "unused:unused@tcp(127.0.0.1:1)/unused", SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := store.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	settings := map[string]string{"ai_provider": "custom", "ai_model": "vendor/model:latest", "ai_base_url": srv.URL + "/v1/chat/completions"}
	err = store.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
		tx.Statement.Build("WHERE")
		if len(tx.Statement.Vars) != 2 {
			t.Errorf("unexpected settings query")
			tx.AddError(gorm.ErrInvalidData)
			return
		}
		key, _ := tx.Statement.Vars[1].(string)
		value, ok := settings[key]
		if !ok {
			tx.AddError(gorm.ErrRecordNotFound)
			return
		}
		row := tx.Statement.Dest.(*models.AppSetting)
		*row = models.AppSetting{SettingKey: key, ValuePlain: value}
		tx.RowsAffected = 1
	})
	if err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = store
	defer func() { db.DB = previous }()
	analyzer := &Analyzer{cfg: &config.Config{AIMaxTokens: 16}}
	p, err := analyzer.getProvider(models.Job{TenantID: "test-tenant"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.AnalyzeChat(context.Background(), "system", "synthetic transcript")
	if err != nil || resp.Provider != "custom" || resp.Model != "vendor/model:latest" {
		t.Fatalf("custom analyzer failed: %v", err)
	}
	if budget < 119*time.Second || budget > 120*time.Second {
		t.Fatalf("default analyzer timeout: %v", budget)
	}
	settings["ai_timeout_seconds"] = "600"
	p, err = analyzer.getProvider(models.Job{TenantID: "test-tenant"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AnalyzeChat(context.Background(), "system", "synthetic transcript"); err != nil {
		t.Fatal(err)
	}
	if budget < 599*time.Second || budget > 600*time.Second {
		t.Fatalf("saved analyzer timeout ignored: %v", budget)
	}
	for _, invalid := range []struct{ key, value string }{{"ai_model", " "}, {"ai_base_url", ""}, {"ai_base_url", "ftp://example.com"}, {"ai_timeout_seconds", "4"}, {"ai_timeout_seconds", "601"}, {"ai_timeout_seconds", "-1"}, {"ai_timeout_seconds", "nonnumeric"}} {
		old := settings[invalid.key]
		settings[invalid.key] = invalid.value
		if _, err := analyzer.getProvider(models.Job{TenantID: "test-tenant"}); err == nil {
			t.Errorf("accepted invalid %s", invalid.key)
		}
		settings[invalid.key] = old
	}
	settings["ai_model"] = ""
	if _, err := analyzer.getProvider(models.Job{TenantID: "test-tenant", AIModel: "gpt-5"}); err == nil {
		t.Error("empty saved custom model silently fell back to the job model")
	}
	settings["ai_provider"] = "openai"
	if _, err := analyzer.getProvider(models.Job{TenantID: "test-tenant"}); err == nil {
		t.Error("builtin allowed missing key")
	}
	if requests != 2 {
		t.Errorf("unexpected transcript requests: %d", requests)
	}
}
