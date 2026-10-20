package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Tham số của riêng phép thử API key — cố ý đặt cực nhỏ để mỗi lần bấm kiểm tra
// chỉ tốn vài token, không liên quan tới cấu hình dùng khi chạy đánh giá thật.
const (
	testKeyMaxTokens    = 16
	testKeySystemPrompt = "Trả lời đúng một từ: OK"
	testKeyUserMessage  = "ping"
)

// GetSettings returns all non-secret settings for the tenant
func GetSettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var settings []models.AppSetting
	db.DB.Where("tenant_id = ?", tenantID).Find(&settings)

	result := make(map[string]string)
	for _, s := range settings {
		if s.ValuePlain != "" {
			result[s.SettingKey] = s.ValuePlain
		} else if len(s.ValueEncrypted) > 0 {
			// Return masked value for encrypted settings
			result[s.SettingKey] = "••••••••"
		}
	}

	// Also get tenant info
	var tenant models.Tenant
	db.DB.First(&tenant, "id = ?", tenantID)

	c.JSON(http.StatusOK, gin.H{
		"settings": result,
		"tenant": gin.H{
			"name":     tenant.Name,
			"timezone": getSettingValue(settings, "timezone", "Asia/Ho_Chi_Minh"),
			"language": getSettingValue(settings, "language", "vi"),
		},
	})
}

func getSettingValue(settings []models.AppSetting, key, defaultVal string) string {
	for _, s := range settings {
		if s.SettingKey == key && s.ValuePlain != "" {
			return s.ValuePlain
		}
	}
	return defaultVal
}

// SaveAISettings saves AI provider and API key
func SaveAISettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req aiSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Printf("[security] loading config failed while saving AI settings: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_error"})
		return
	}

	saved, err := loadSavedAISettings(tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_error"})
		return
	}
	resolved, err := resolveAISettings(&req, saved, cfg.EncryptionKey, true)
	if err != nil {
		aiConfigError(c, err)
		return
	}
	previous, previousErr := resolveAISettings(nil, saved, cfg.EncryptionKey, false)
	invalidateCache := previousErr != nil || modelCacheIdentity(previous) != modelCacheIdentity(resolved)
	// Validate and encrypt everything before the first write. A transaction also
	// prevents storage errors from leaving a partially updated configuration.
	var encrypted []byte
	if resolved.APIKey != "" {
		encrypted, err = pkg.Encrypt([]byte(resolved.APIKey), cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
			return
		}
	}
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		values := map[string]string{"ai_provider": resolved.Provider, "ai_base_url": resolved.BaseURL}
		if resolved.Provider == "custom" {
			values["ai_timeout_seconds"] = strconv.Itoa(resolved.TimeoutSeconds)
		}
		if resolved.Model != "" {
			values["ai_model"] = resolved.Model
		}
		if req.BatchMode != "" {
			values["ai_batch_mode"] = req.BatchMode
		}
		if req.BatchSize != "" {
			values["ai_batch_size"] = req.BatchSize
		}
		for key, value := range values {
			if err := writeAISetting(tx, tenantID, key, value, nil); err != nil {
				return err
			}
		}
		if encrypted == nil {
			if err := tx.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_api_key").Delete(&models.AppSetting{}).Error; err != nil {
				return err
			}
		} else if err := writeAISetting(tx, tenantID, "ai_api_key", "", encrypted); err != nil {
			return err
		}
		// Cache identity also guards against an in-flight refresh of the old config.
		if invalidateCache {
			return tx.Where("tenant_id = ? AND setting_key IN ?", tenantID, []string{settingModelsCache, settingModelsFetched}).Delete(&models.AppSetting{}).Error
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

func writeAISetting(tx *gorm.DB, tenantID, key, plain string, encrypted []byte) error {
	now := time.Now()
	row := models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: key, ValuePlain: plain, ValueEncrypted: encrypted, CreatedAt: now, UpdatedAt: now}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value_plain", "value_encrypted", "updated_at"}),
	}).Create(&row).Error
}

// SaveAnalysisSettings saves batch mode and batch size settings
func SaveAnalysisSettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		BatchMode string `json:"batch_mode" binding:"required"`
		BatchSize string `json:"batch_size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	upsertSetting(tenantID, "ai_batch_mode", req.BatchMode, nil)
	if req.BatchSize != "" {
		upsertSetting(tenantID, "ai_batch_size", req.BatchSize, nil)
	}

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// testKeyTimeout giới hạn thời gian chờ khi thử API key, để nút kiểm tra không
// treo quá lâu khi nhà cung cấp chậm hoặc đang chặn vì hết hạn mức.
const testKeyTimeout = 30 * time.Second

// classifyProviderError quy lỗi từ nhà cung cấp về một lý do dễ hiểu cho người
// dùng. Không trả nguyên văn lỗi ra ngoài để tránh lộ chi tiết nội bộ.
func classifyProviderError(err error) (code string, message string) {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "401") || strings.Contains(msg, "403") ||
		strings.Contains(msg, "unauthenticated") || strings.Contains(msg, "permission") ||
		strings.Contains(msg, "api key not valid") || strings.Contains(msg, "invalid_api_key") ||
		strings.Contains(msg, "incorrect api key") || strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "api key") || strings.Contains(msg, "authentication"):
		return "invalid_api_key", "API key không hợp lệ hoặc không có quyền truy cập"
	case strings.Contains(msg, "429") || strings.Contains(msg, "quota") ||
		strings.Contains(msg, "rate") || strings.Contains(msg, "resource_exhausted"):
		return "quota_exceeded", "API key đã hết hạn mức hoặc bị giới hạn tần suất"
	case strings.Contains(msg, "404") || strings.Contains(msg, "not found") ||
		strings.Contains(msg, "model"):
		return "model_unavailable", "Model đang chọn không dùng được với API key này"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") ||
		strings.Contains(msg, "connection") || strings.Contains(msg, "no such host"):
		return "connection_failed", "Không kết nối được tới nhà cung cấp"
	default:
		return "test_failed", "Gọi thử tới nhà cung cấp không thành công"
	}
}

// TestAIKey thử API key bằng một lượt gọi thật tới nhà cung cấp.
//
// Trước đây hàm này chỉ kiểm tra có key trong database rồi báo thành công, nên
// key sai hay hết hạn mức vẫn hiện xanh và người dùng chỉ phát hiện khi công
// việc chạy thật mà không ra kết quả.
func TestAIKey(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	draft, err := decodeAIDraft(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	cfg, err := config.Load()
	if err != nil {
		log.Printf("[security] loading config failed while testing AI key: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_error"})
		return
	}

	saved, err := loadSavedAISettings(tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_error"})
		return
	}
	resolved, err := resolveAISettings(draft, saved, cfg.EncryptionKey, true)
	if err != nil {
		if draft == nil && err.Error() == "api_key_required" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no_api_key_configured", "message": "Chưa cấu hình API key"})
		} else {
			aiConfigError(c, err)
		}
		return
	}
	provider, model := resolved.Provider, resolved.Model
	client, err := newAITestProvider(resolved)
	if err != nil {
		aiConfigError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), aiTestTimeout(resolved))
	defer cancel()

	resp, err := client.AnalyzeChat(ctx, testKeySystemPrompt, testKeyUserMessage)
	if err != nil {
		code, message := classifyProviderError(err)
		log.Printf("[ai] test key failed: tenant=%s provider=%s reason=%s", tenantID, provider, code)
		c.JSON(http.StatusBadRequest, gin.H{"error": code, "message": message, "provider": provider})
		return
	}

	usedModel := resp.Model
	if usedModel == "" {
		usedModel = model
	}
	c.JSON(http.StatusOK, gin.H{
		"status":   "ok",
		"provider": provider,
		"model":    usedModel,
		"message":  "Kết nối thành công",
	})
}

// SaveGeneralSettings saves general tenant settings
func SaveGeneralSettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		CompanyName  string  `json:"company_name"`
		Timezone     string  `json:"timezone"`
		Language     string  `json:"language"`
		ExchangeRate float64 `json:"exchange_rate_vnd"`
		AppURL       string  `json:"app_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// Update tenant name
	if req.CompanyName != "" {
		db.DB.Model(&models.Tenant{}).Where("id = ?", tenantID).Updates(map[string]interface{}{
			"name":       req.CompanyName,
			"updated_at": time.Now(),
		})
	}

	// Save timezone and language as settings
	if req.Timezone != "" {
		upsertSetting(tenantID, "timezone", req.Timezone, nil)
	}
	if req.Language != "" {
		upsertSetting(tenantID, "language", req.Language, nil)
	}
	if req.ExchangeRate > 0 {
		upsertSetting(tenantID, "exchange_rate_vnd", fmt.Sprintf("%.0f", req.ExchangeRate), nil)
	}

	// Strip trailing slash from app URL
	appURL := strings.TrimRight(req.AppURL, "/")
	upsertSetting(tenantID, "app_url", appURL, nil)

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// ChangePassword changes the user's password
func ChangePassword(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var req struct {
		CurrentPassword string `json:"current_password" binding:"required"`
		NewPassword     string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}
	if err := validatePasswordComplexity(req.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "weak_password", "message": err.Error()})
		return
	}

	var user models.User
	if err := db.DB.First(&user, "id = ?", userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user_not_found"})
		return
	}

	// Verify current password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "wrong_current_password"})
		return
	}

	// Hash new password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash_failed"})
		return
	}

	if err := db.DB.Model(&user).Updates(map[string]interface{}{
		"password_hash": string(hash),
		"updated_at":    time.Now(),
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update_failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "password_changed"})
}

// allowedSettingKeys is a whitelist of keys that can be set via the SaveSetting API.
// Sensitive keys like ai_api_key must be set through dedicated endpoints.
var allowedSettingKeys = map[string]bool{
	"onboarding_dismissed": true,
	"language":             true,
	"timezone":             true,
	"date_format":          true,
	"notification_enabled": true,
	"sync_interval":        true,
	"default_ai_provider":  true,
	"default_ai_model":     true,
}

// SaveSetting saves a single key-value setting
func SaveSetting(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		Key   string `json:"key" binding:"required"`
		Value string `json:"value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if !allowedSettingKeys[req.Key] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "setting_key_not_allowed"})
		return
	}
	upsertSetting(tenantID, req.Key, req.Value, nil)
	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

func upsertSetting(tenantID, key, plainValue string, encryptedValue []byte) {
	var existing models.AppSetting
	result := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&existing)

	if result.Error == nil {
		// Update
		updates := map[string]interface{}{"updated_at": time.Now()}
		if plainValue != "" {
			updates["value_plain"] = plainValue
			updates["value_encrypted"] = nil
		}
		if encryptedValue != nil {
			updates["value_encrypted"] = encryptedValue
			updates["value_plain"] = ""
		}
		db.DB.Model(&existing).Updates(updates)
	} else {
		// Create
		setting := models.AppSetting{
			ID:             pkg.NewUUID(),
			TenantID:       tenantID,
			SettingKey:     key,
			ValuePlain:     plainValue,
			ValueEncrypted: encryptedValue,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		db.DB.Create(&setting)
	}
}
