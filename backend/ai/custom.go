package ai

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultCustomTimeoutSeconds = 120

// NormalizeCustomTimeoutSeconds validates the user-facing timeout setting.
func NormalizeCustomTimeoutSeconds(seconds int) (int, error) {
	if seconds == 0 {
		return DefaultCustomTimeoutSeconds, nil
	}
	if seconds < 5 || seconds > 600 {
		return 0, fmt.Errorf("invalid_timeout")
	}
	return seconds, nil
}

// ParseCustomTimeoutSeconds also accepts missing legacy settings as the default.
func ParseCustomTimeoutSeconds(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultCustomTimeoutSeconds, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid_timeout")
	}
	return NormalizeCustomTimeoutSeconds(seconds)
}

// NormalizeCustomEndpoint accepts a base URL or a complete chat endpoint.
// Queries are preserved verbatim; credentials and fragments are never accepted.
func NormalizeCustomEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.Contains(endpoint, "#") {
		return "", fmt.Errorf("custom endpoint must be an HTTP(S) URL without credentials or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")
	if !strings.HasSuffix(u.Path, "/chat/completions") {
		u.Path += "/chat/completions"
		if u.RawPath != "" {
			u.RawPath += "/chat/completions"
		}
	}
	return u.String(), nil
}

// NewCustomProvider deliberately has no public endpoint or model defaults.
func NewCustomProvider(apiKey, model string, maxTokens int, endpoint string) (*OpenAICompatibleProvider, error) {
	return NewCustomProviderWithTimeout(apiKey, model, maxTokens, endpoint, DefaultCustomTimeoutSeconds*time.Second)
}

// NewCustomProviderWithTimeout bounds the entire analysis, including retries.
// Settings validate whole seconds separately; positive subsecond durations are
// supported here for callers that need a tighter deadline.
func NewCustomProviderWithTimeout(apiKey, model string, maxTokens int, endpoint string, timeout time.Duration) (*OpenAICompatibleProvider, error) {
	if timeout == 0 {
		timeout = DefaultCustomTimeoutSeconds * time.Second
	}
	if timeout < 0 {
		return nil, fmt.Errorf("invalid_timeout")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("custom model is required")
	}
	normalized, err := NormalizeCustomEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	p := newOpenAICompatible("custom", strings.TrimSpace(apiKey), model, maxTokens, normalized)
	p.chatEndpoint = normalized
	p.timeout = timeout
	return p, nil
}
