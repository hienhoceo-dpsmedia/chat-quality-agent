package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

type aiSettingsRequest struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	ClearAPIKey    bool   `json:"clear_api_key"`
	BatchMode      string `json:"batch_mode"`
	BatchSize      string `json:"batch_size"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type savedAISettings struct {
	Config       aiSettingsRequest
	EncryptedKey []byte
	TimeoutRaw   string // parsed only in saved mode; a draft may repair invalid settings
}

// decodeAIDraft distinguishes no body (saved mode) from an explicit draft.
func decodeAIDraft(body io.Reader) (*aiSettingsRequest, error) {
	if body == nil {
		return nil, nil
	}
	decoder := json.NewDecoder(body)
	var draft *aiSettingsRequest
	if err := decoder.Decode(&draft); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	if draft == nil {
		return nil, errors.New("invalid_request")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid_request")
	}
	return draft, nil
}

func loadSavedAISettings(tenantID string) (savedAISettings, error) {
	var rows []models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key IN ?", tenantID, []string{"ai_provider", "ai_model", "ai_base_url", "ai_api_key", "ai_timeout_seconds"}).Find(&rows).Error; err != nil {
		return savedAISettings{}, err
	}
	saved := savedAISettings{Config: aiSettingsRequest{Provider: "claude"}}
	for _, row := range rows {
		switch row.SettingKey {
		case "ai_provider":
			if row.ValuePlain != "" {
				saved.Config.Provider = row.ValuePlain
			}
		case "ai_model":
			saved.Config.Model = row.ValuePlain
		case "ai_base_url":
			saved.Config.BaseURL = row.ValuePlain
		case "ai_api_key":
			saved.Config.APIKey, saved.EncryptedKey = row.ValuePlain, row.ValueEncrypted
		case "ai_timeout_seconds":
			saved.TimeoutRaw = row.ValuePlain
		}
	}
	return saved, nil
}

func credentialEndpoint(provider, endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if provider == "custom" {
		if normalized, err := ai.NormalizeCustomEndpoint(endpoint); err == nil {
			return normalized
		}
	}
	if endpoint == "" {
		switch provider {
		case "claude":
			endpoint = "https://api.anthropic.com"
		case "gemini":
			endpoint = "https://generativelanguage.googleapis.com"
		case "openai":
			endpoint = "https://api.openai.com/v1"
		case "xai":
			endpoint = "https://api.x.ai/v1"
		}
	}
	return strings.TrimRight(endpoint, "/")
}

// resolveAISettings is pure: drafts never write settings or mutate saved state.
// Decrypt only when reuse is allowed, so replacing a broken key still works.
func resolveAISettings(draft *aiSettingsRequest, saved savedAISettings, encryptionKey string, requireModel bool) (aiSettingsRequest, error) {
	resolved := saved.Config
	if draft != nil {
		resolved = *draft
	}
	resolved.Provider = strings.TrimSpace(resolved.Provider)
	resolved.Model = strings.TrimSpace(resolved.Model)
	resolved.BaseURL = strings.TrimSpace(resolved.BaseURL)
	resolved.APIKey = strings.TrimSpace(resolved.APIKey)
	switch resolved.Provider {
	case "custom":
		var err error
		if draft == nil && saved.TimeoutRaw != "" {
			resolved.TimeoutSeconds, err = ai.ParseCustomTimeoutSeconds(saved.TimeoutRaw)
		} else {
			resolved.TimeoutSeconds, err = ai.NormalizeCustomTimeoutSeconds(resolved.TimeoutSeconds)
		}
		if err != nil {
			return aiSettingsRequest{}, err
		}
		normalized, err := ai.NormalizeCustomEndpoint(resolved.BaseURL)
		if err != nil {
			return aiSettingsRequest{}, errors.New("invalid_base_url")
		}
		resolved.BaseURL = normalized
		if requireModel && resolved.Model == "" {
			return aiSettingsRequest{}, errors.New("model_required")
		}
	case "claude", "gemini", "openai", "xai":
	default:
		return aiSettingsRequest{}, errors.New("unsupported_provider")
	}
	sameTarget := resolved.Provider == saved.Config.Provider && credentialEndpoint(resolved.Provider, resolved.BaseURL) == credentialEndpoint(saved.Config.Provider, saved.Config.BaseURL)
	if resolved.ClearAPIKey {
		resolved.APIKey = ""
	} else if resolved.APIKey == "" {
		// Initial setup may be keyless. Switching an existing configuration
		// requires explicit intent even if its previous endpoint was keyless.
		hasSavedConfig := len(saved.EncryptedKey) > 0 || saved.Config.APIKey != "" || saved.Config.BaseURL != "" || saved.Config.Model != ""
		if !sameTarget && hasSavedConfig {
			return aiSettingsRequest{}, errors.New("api_key_required")
		}
		if sameTarget {
			resolved.APIKey = strings.TrimSpace(saved.Config.APIKey)
			if len(saved.EncryptedKey) > 0 {
				key, err := pkg.Decrypt(saved.EncryptedKey, encryptionKey)
				if err != nil {
					return aiSettingsRequest{}, errors.New("decrypt_failed")
				}
				resolved.APIKey = strings.TrimSpace(string(key))
			}
		}
	}
	if resolved.Provider != "custom" && resolved.APIKey == "" {
		return aiSettingsRequest{}, errors.New("api_key_required")
	}
	return resolved, nil
}

func aiConfigError(c *gin.Context, err error) {
	code := err.Error()
	message := map[string]string{
		"invalid_base_url":     "Endpoint phải là URL HTTP(S) hợp lệ, không chứa tài khoản hoặc fragment",
		"model_required":       "Vui lòng nhập model cho Custom AI",
		"invalid_timeout":      "Thời gian chờ Custom AI phải từ 5 đến 600 giây (mặc định 120 giây)",
		"api_key_required":     "Vui lòng nhập API key; khi đổi nhà cung cấp hoặc endpoint, nhập key mới hoặc chọn xóa key cho Custom AI",
		"decrypt_failed":       "Không giải mã được API key đã lưu, vui lòng nhập lại",
		"unsupported_provider": "Nhà cung cấp không được hỗ trợ",
	}[code]
	if message == "" {
		code, message = "config_error", "Không đọc được cấu hình AI"
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": code, "message": message})
}

func newAITestProvider(cfg aiSettingsRequest) (ai.AIProvider, error) {
	switch cfg.Provider {
	case "claude":
		return ai.NewClaudeProvider(cfg.APIKey, cfg.Model, testKeyMaxTokens, cfg.BaseURL), nil
	case "gemini":
		return ai.NewGeminiProvider(cfg.APIKey, cfg.Model, cfg.BaseURL), nil
	case "openai":
		return ai.NewOpenAIProvider(cfg.APIKey, cfg.Model, testKeyMaxTokens, cfg.BaseURL), nil
	case "xai":
		return ai.NewXAIProvider(cfg.APIKey, cfg.Model, testKeyMaxTokens, cfg.BaseURL), nil
	case "custom":
		return ai.NewCustomProviderWithTimeout(cfg.APIKey, cfg.Model, testKeyMaxTokens, cfg.BaseURL, aiTestTimeout(cfg))
	default:
		return nil, errors.New("unsupported_provider")
	}
}

func aiTestTimeout(cfg aiSettingsRequest) time.Duration {
	if cfg.Provider == "custom" {
		return time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	return testKeyTimeout
}

// Only a digest is persisted, never plaintext credentials or routing parameters.
func modelCacheIdentity(cfg aiSettingsRequest) string {
	payload, _ := json.Marshal([]string{cfg.Provider, credentialEndpoint(cfg.Provider, cfg.BaseURL), cfg.APIKey})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
