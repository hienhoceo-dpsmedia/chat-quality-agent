package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai/catalog"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// Danh sách model đổi rất hiếm, nên bản lưu dùng được cả ngày.
const modelCacheTTL = 24 * time.Hour

const (
	settingModelsCache   = "ai_models_cache"
	settingModelsFetched = "ai_models_fetched_at"
)

type modelListResponse struct {
	Provider  string          `json:"provider"`
	Models    []catalog.Model `json:"models"`
	Source    string          `json:"source"` // provider | cache | static
	FetchedAt string          `json:"fetched_at,omitempty"`
	Message   string          `json:"message,omitempty"`
}

// Danh sách dự phòng khi chưa từng lấy được từ nhà cung cấp.
var staticClaudeModels = []catalog.Model{
	{ID: "claude-sonnet-5", Title: "Claude Sonnet 5 (Khuyến nghị)"},
	{ID: "claude-haiku-4-5", Title: "Claude Haiku 4.5 (Nhanh & rẻ)"},
	{ID: "claude-opus-5", Title: "Claude Opus 5 (Mạnh nhất)"},
	{ID: "claude-sonnet-4-6", Title: "Claude Sonnet 4.6 (Thế hệ cũ)"},
	{ID: "claude-opus-4-6", Title: "Claude Opus 4.6 (Thế hệ cũ)"},
}

var staticGeminiModels = []catalog.Model{
	{ID: "gemini-3.8-flash", Title: "Gemini 3.8 Flash (Khuyến nghị)"},
	{ID: "gemini-3.1-flash-lite", Title: "Gemini 3.1 Flash Lite (Nhanh & rẻ nhất)"},
	{ID: "gemini-3.5-flash-lite", Title: "Gemini 3.5 Flash Lite (Rẻ)"},
	{ID: "gemini-3.5-flash", Title: "Gemini 3.5 Flash (Mạnh hơn)"},
	{ID: "gemini-2.5-pro", Title: "Gemini 2.5 Pro (Thế hệ cũ)"},
	{ID: "gemini-2.5-flash", Title: "Gemini 2.5 Flash (Thế hệ cũ)"},
}

var staticOpenAIModels = []catalog.Model{
	{ID: "gpt-5", Title: "GPT-5 (Khuyến nghị)"},
	{ID: "gpt-5-mini", Title: "GPT-5 mini (Nhanh & rẻ)"},
	{ID: "o3", Title: "o3 (Suy luận sâu)"},
}

var staticXAIModels = []catalog.Model{
	{ID: "grok-4", Title: "Grok 4 (Khuyến nghị)"},
	{ID: "grok-3", Title: "Grok 3 (Thế hệ cũ)"},
}

func staticModels(provider string) []catalog.Model {
	switch provider {
	case "custom":
		return []catalog.Model{}
	case "gemini":
		return staticGeminiModels
	case "openai":
		return staticOpenAIModels
	case "xai":
		return staticXAIModels
	default:
		return staticClaudeModels
	}
}

// ListAIModels trả danh sách model cho phần cấu hình AI.
//
// Trả ngay bản đã lưu để trang mở không phải chờ mạng; bản lưu quá hạn thì làm
// mới ngầm cho lần sau. Model đang được chọn luôn có mặt trong danh sách, kể cả
// khi nhà cung cấp đã gỡ nó đi.
func ListAIModels(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	saved, err := loadSavedAISettings(tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_error"})
		return
	}
	provider, currentModel := saved.Config.Provider, saved.Config.Model
	cfg, err := resolveModelSettings(nil, saved)
	if err != nil {
		respondModelFallback(c, provider, currentModel)
		return
	}

	cached, fetchedAt := readModelCache(tenantID, cfg)
	if len(cached) > 0 {
		fresh := time.Since(fetchedAt) < modelCacheTTL
		if !fresh {
			// Làm mới ngầm, người dùng vẫn nhận danh sách cũ ngay lập tức.
			go refreshModelsInBackground(tenantID, cfg)
		}
		c.JSON(http.StatusOK, modelListResponse{
			Provider:  provider,
			Models:    catalog.EnsureContains(cached, currentModel),
			Source:    "cache",
			FetchedAt: fetchedAt.Format(time.RFC3339),
		})
		return
	}

	// Chưa có bản lưu nào: thử lấy ngay, hỏng thì dùng danh sách dự phòng.
	fetched, err := fetchModels(c.Request.Context(), cfg)
	if err != nil {
		respondModelFallback(c, provider, currentModel)
		return
	}

	saveModelCache(tenantID, cfg, fetched)
	c.JSON(http.StatusOK, modelListResponse{
		Provider:  provider,
		Models:    catalog.EnsureContains(fetched, currentModel),
		Source:    "provider",
		FetchedAt: time.Now().Format(time.RFC3339),
	})
}

func respondModelFallback(c *gin.Context, provider, currentModel string) {
	message := "Chưa lấy được danh sách từ nhà cung cấp, đang hiển thị danh sách có sẵn"
	if provider == "custom" {
		message = "Chưa lấy được danh sách model; bạn vẫn có thể nhập model thủ công"
	}
	c.JSON(http.StatusOK, modelListResponse{Provider: provider, Models: catalog.EnsureContains(staticModels(provider), currentModel), Source: "static", Message: message})
}

// RefreshAIModels ép lấy lại danh sách ngay, phục vụ nút Làm mới.
func RefreshAIModels(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	draft, err := decodeAIDraft(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	saved, err := loadSavedAISettings(tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_error"})
		return
	}
	cfg, err := resolveModelSettings(draft, saved)
	if err != nil {
		aiConfigError(c, err)
		return
	}
	provider, currentModel := cfg.Provider, cfg.Model

	fetched, err := fetchModels(c.Request.Context(), cfg)
	if err != nil {
		code, message := classifyProviderError(err)
		// classifyProviderError viết cho việc thử key; với việc lấy danh sách thì
		// lỗi chung chung cần câu chữ đúng ngữ cảnh hơn.
		if code == "test_failed" {
			code = "model_list_failed"
			message = "Không lấy được danh sách model từ nhà cung cấp. Kiểm tra lại API key và kết nối mạng"
		}
		log.Printf("[ai] làm mới danh sách model thất bại: tenant=%s provider=%s reason=%s", tenantID, provider, code)
		c.JSON(http.StatusBadRequest, gin.H{"error": code, "message": message})
		return
	}

	// Draft discovery must not persist configuration or poison the saved cache.
	if draft == nil {
		saveModelCache(tenantID, cfg, fetched)
	}
	c.JSON(http.StatusOK, modelListResponse{
		Provider:  provider,
		Models:    catalog.EnsureContains(fetched, currentModel),
		Source:    "provider",
		FetchedAt: time.Now().Format(time.RFC3339),
	})
}

func resolveModelSettings(draft *aiSettingsRequest, saved savedAISettings) (aiSettingsRequest, error) {
	cfg, err := config.Load()
	if err != nil {
		return aiSettingsRequest{}, err
	}
	return resolveAISettings(draft, saved, cfg.EncryptionKey, false)
}

func fetchModels(ctx context.Context, cfg aiSettingsRequest) ([]catalog.Model, error) {
	switch cfg.Provider {
	case "custom":
		return catalog.FetchCustom(ctx, cfg.APIKey, cfg.BaseURL)
	case "gemini":
		return catalog.FetchGemini(ctx, cfg.APIKey, cfg.BaseURL)
	case "openai":
		return catalog.FetchOpenAICompatible(ctx, cfg.APIKey, cfg.BaseURL, "https://api.openai.com/v1")
	case "xai":
		return catalog.FetchOpenAICompatible(ctx, cfg.APIKey, cfg.BaseURL, "https://api.x.ai/v1")
	default:
		return catalog.FetchClaude(ctx, cfg.APIKey, cfg.BaseURL)
	}
}

func refreshModelsInBackground(tenantID string, cfg aiSettingsRequest) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[security] panic recovered khi làm mới danh sách model: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fetched, err := fetchModels(ctx, cfg)
	if err != nil {
		log.Printf("[ai] làm mới ngầm danh sách model thất bại: tenant=%s provider=%s", tenantID, cfg.Provider)
		return
	}
	saveModelCache(tenantID, cfg, fetched)
}

func readModelCache(tenantID string, cfg aiSettingsRequest) ([]catalog.Model, time.Time) {
	raw := settingValue(tenantID, settingModelsCache)
	if raw == "" {
		return nil, time.Time{}
	}
	var stored struct {
		Provider string          `json:"provider"`
		Identity string          `json:"identity"`
		Models   []catalog.Model `json:"models"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, time.Time{}
	}
	// Đổi nhà cung cấp thì bản lưu cũ không dùng được.
	if stored.Provider != cfg.Provider || stored.Identity != modelCacheIdentity(cfg) {
		return nil, time.Time{}
	}
	fetchedAt, err := time.Parse(time.RFC3339, settingValue(tenantID, settingModelsFetched))
	if err != nil {
		return stored.Models, time.Time{}
	}
	return stored.Models, fetchedAt
}

func saveModelCache(tenantID string, cfg aiSettingsRequest, list []catalog.Model) {
	payload, err := json.Marshal(struct {
		Provider string          `json:"provider"`
		Identity string          `json:"identity"`
		Models   []catalog.Model `json:"models"`
	}{cfg.Provider, modelCacheIdentity(cfg), list})
	if err != nil {
		log.Printf("[ai] không lưu được danh sách model: %v", err)
		return
	}
	upsertSetting(tenantID, settingModelsCache, string(payload), nil)
	upsertSetting(tenantID, settingModelsFetched, time.Now().Format(time.RFC3339), nil)
}

func settingValue(tenantID, key string) string {
	var s models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&s).Error; err != nil {
		return ""
	}
	return s.ValuePlain
}
