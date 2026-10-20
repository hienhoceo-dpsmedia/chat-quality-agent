package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatibleProvider nói chuyện với bất kỳ dịch vụ nào theo chuẩn
// /chat/completions của OpenAI — dùng được cho OpenAI, xAI (Grok), và các proxy
// như OpenRouter hay LiteLLM.
//
// Cố ý gọi HTTP trực tiếp thay vì thêm SDK: phần API dùng tới chỉ là một endpoint
// với vài trường, và mỗi SDK lại ràng buộc thêm một nhà cung cấp cụ thể.
type OpenAICompatibleProvider struct {
	providerName string
	apiKey       string
	model        string
	baseURL      string
	chatEndpoint string // normalized full endpoint, custom only
	maxTokens    int
	timeout      time.Duration // entire request/retry budget, custom only
}

const (
	openAIDefaultBaseURL = "https://api.openai.com/v1"
	xaiDefaultBaseURL    = "https://api.x.ai/v1"
	// Phản hồi chấm chat không bao giờ dài tới mức này; chặn để một phản hồi
	// bất thường không nuốt hết bộ nhớ.
	maxCompletionResponseSize = 8 << 20
)

// NewOpenAIProvider tạo provider cho OpenAI.
func NewOpenAIProvider(apiKey, model string, maxTokens int, baseURL string) *OpenAICompatibleProvider {
	if model == "" {
		model = "gpt-5"
	}
	if baseURL == "" {
		baseURL = openAIDefaultBaseURL
	}
	return newOpenAICompatible("openai", apiKey, model, maxTokens, baseURL)
}

// NewXAIProvider tạo provider cho xAI (Grok).
func NewXAIProvider(apiKey, model string, maxTokens int, baseURL string) *OpenAICompatibleProvider {
	if model == "" {
		model = "grok-4"
	}
	if baseURL == "" {
		baseURL = xaiDefaultBaseURL
	}
	return newOpenAICompatible("xai", apiKey, model, maxTokens, baseURL)
}

func newOpenAICompatible(name, apiKey, model string, maxTokens int, baseURL string) *OpenAICompatibleProvider {
	if maxTokens <= 0 {
		maxTokens = 16384
	}
	return &OpenAICompatibleProvider{
		providerName: name,
		apiKey:       apiKey,
		model:        model,
		baseURL:      strings.TrimSuffix(baseURL, "/"),
		maxTokens:    maxTokens,
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	// OpenAI trả error là object, xAI trả là chuỗi — nhận cả hai dạng.
	Error json.RawMessage `json:"error"`
}

// errorMessage rút thông báo lỗi ra khỏi phần error, bất kể nó là chuỗi hay object.
func (r chatCompletionResponse) errorMessage() string {
	if len(r.Error) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(r.Error, &asString); err == nil {
		return asString
	}
	var asObject struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(r.Error, &asObject); err == nil {
		return asObject.Message
	}
	return ""
}

func (p *OpenAICompatibleProvider) AnalyzeChat(ctx context.Context, systemPrompt string, chatTranscript string) (AIResponse, error) {
	if p.providerName == "custom" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}
	return withRetry(ctx, p.providerName, func() (AIResponse, error) {
		body := map[string]any{
			"model": p.model,
			"messages": []chatMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: chatTranscript},
			},
		}
		// OpenAI đã đổi sang max_completion_tokens và từ chối max_tokens trên các
		// model đời mới; xAI và phần lớn proxy vẫn dùng tên cũ.
		if p.providerName == "openai" {
			body["max_completion_tokens"] = p.maxTokens
		} else {
			body["max_tokens"] = p.maxTokens
		}

		payload, err := json.Marshal(body)
		if err != nil {
			return AIResponse{}, fmt.Errorf("%s marshal request: %w", p.providerName, err)
		}

		endpoint := p.baseURL + "/chat/completions"
		if p.chatEndpoint != "" {
			endpoint = p.chatEndpoint
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return AIResponse{}, fmt.Errorf("%s create request: %w", p.providerName, err)
		}
		req.Header.Set("Content-Type", "application/json")
		if p.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.apiKey)
		}

		client := NewHTTPClientWithTimeout()
		if p.providerName == "custom" {
			client.Timeout = p.timeout
		}
		resp, err := client.Do(req)
		if err != nil {
			return AIResponse{}, fmt.Errorf("%s api error: %w", p.providerName, err)
		}
		defer resp.Body.Close()

		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCompletionResponseSize))
		if err != nil {
			return AIResponse{}, fmt.Errorf("%s read response: %w", p.providerName, err)
		}

		var parsed chatCompletionResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			// Mã lỗi phải nằm trong thông báo để lớp thử lại nhận ra lỗi tạm thời.
			return AIResponse{}, fmt.Errorf("%s api error: mã %d, phản hồi không đọc được", p.providerName, resp.StatusCode)
		}

		if resp.StatusCode != http.StatusOK {
			return AIResponse{}, fmt.Errorf("%s api error: mã %d %s", p.providerName, resp.StatusCode, parsed.errorMessage())
		}

		if len(parsed.Choices) == 0 {
			return AIResponse{}, fmt.Errorf("%s api returned no choices", p.providerName)
		}
		choice := parsed.Choices[0]
		if choice.Message.Refusal != "" {
			return AIResponse{}, fmt.Errorf("%s từ chối yêu cầu: %s", p.providerName, choice.Message.Refusal)
		}
		if strings.TrimSpace(choice.Message.Content) == "" {
			return AIResponse{}, fmt.Errorf("%s api returned empty content", p.providerName)
		}

		return AIResponse{
			Content:      choice.Message.Content,
			Model:        p.model,
			Provider:     p.providerName,
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
		}, nil
	})
}

func (p *OpenAICompatibleProvider) AnalyzeChatBatch(ctx context.Context, systemPrompt string, items []BatchItem) (AIResponse, error) {
	batchPrompt := WrapBatchPrompt(systemPrompt, len(items))
	batchTranscript := FormatBatchTranscript(items)
	return p.AnalyzeChat(ctx, batchPrompt, batchTranscript)
}
