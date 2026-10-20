package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Chatwoot trả 25 hội thoại mỗi trang khi không truyền per_page.
	chatwootPageSize = 25

	// Giới hạn phần thân lỗi để log không phình to.
	chatwootMaxErrorBody = 512
)

type ChatwootCredentials struct {
	BaseURL   string `json:"base_url"`
	AccountID int    `json:"account_id"`
	APIToken  string `json:"api_token"`
	InboxIDs  []int  `json:"inbox_ids"`
}

type ChatwootAdapter struct {
	creds  ChatwootCredentials
	client *http.Client

	agentMu sync.Mutex
	agents  map[int]string // chatwoot user id -> email
}

func NewChatwootAdapter(creds ChatwootCredentials) *ChatwootAdapter {
	return &ChatwootAdapter{
		creds:  creds,
		client: &http.Client{Timeout: 30 * time.Second},
		agents: map[int]string{},
	}
}

func (c *ChatwootAdapter) url(path string, q url.Values) string {
	base := strings.TrimRight(c.creds.BaseURL, "/")
	u := fmt.Sprintf("%s/api/v1/accounts/%d%s", base, c.creds.AccountID, path)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (c *ChatwootAdapter) doJSON(ctx context.Context, method, target string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("api_access_token", c.creds.APIToken)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chatwoot %s %s: status %d: %s", method, target, resp.StatusCode, errorBody(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// errorBody cắt ngắn phần thân phản hồi lỗi của Chatwoot.
func errorBody(body []byte) string {
	if len(body) > chatwootMaxErrorBody {
		return string(body[:chatwootMaxErrorBody]) + "..."
	}
	return string(body)
}

type chatwootConversation struct {
	// API trả id là display_id (jbuilder ghi đè), không phải id nội bộ.
	DisplayID      int    `json:"id"`
	LastActivityAt int64  `json:"last_activity_at"`
	InboxID        int    `json:"inbox_id"`
	Status         string `json:"status"`
	Meta           struct {
		Sender struct {
			Name string `json:"name"`
		} `json:"sender"`
		Assignee *struct {
			Name string `json:"name"`
		} `json:"assignee"`
		Team *struct {
			Name string `json:"name"`
		} `json:"team"`
	} `json:"meta"`
}

type chatwootConversationList struct {
	Data struct {
		Payload []chatwootConversation `json:"payload"`
	} `json:"data"`
}

// FetchRecentConversations lấy hội thoại hoạt động kể từ `since`.
// `limit` là giới hạn cho MỖI inbox, không phải tổng; limit <= 0 nghĩa là 100.
// Lỗi ở một inbox không làm hỏng cả lượt đồng bộ: inbox đó dừng lại và các
// inbox còn lại vẫn được lấy.
func (c *ChatwootAdapter) FetchRecentConversations(ctx context.Context, since time.Time, limit int) ([]SyncedConversation, error) {
	if limit <= 0 {
		limit = 100
	}
	inboxes := c.creds.InboxIDs
	if len(inboxes) == 0 {
		inboxes = []int{0}
	}
	seen := map[int]bool{}
	out := make([]SyncedConversation, 0, limit)
	var lastErr error
	for _, inboxID := range inboxes {
		collected := 0
		for page := 1; ; page++ {
			q := url.Values{
				"status":  {"all"},
				"sort_by": {"last_activity_at_desc"},
				"page":    {strconv.Itoa(page)},
			}
			if inboxID > 0 {
				q.Set("inbox_id", strconv.Itoa(inboxID))
			}
			var resp chatwootConversationList
			if err := c.doJSON(ctx, http.MethodGet, c.url("/conversations", q), &resp); err != nil {
				lastErr = fmt.Errorf("chatwoot conversations inbox %d page %d: %w", inboxID, page, err)
				log.Printf("[chatwoot] inbox %d page %d: %v", inboxID, page, err)
				break
			}
			payload := resp.Data.Payload
			if len(payload) == 0 {
				break
			}
			olderFound := false
			added := 0
			for _, conv := range payload {
				// Bỏ hội thoại chưa có mốc hoạt động thay vì ghi mốc 1970.
				if conv.LastActivityAt <= 0 {
					continue
				}
				if time.Unix(conv.LastActivityAt, 0).Before(since) {
					olderFound = true
					break
				}
				if seen[conv.DisplayID] {
					continue
				}
				seen[conv.DisplayID] = true
				out = append(out, toSyncedConversation(conv))
				added++
				collected++
				if collected >= limit {
					break
				}
			}
			if collected >= limit || olderFound {
				break
			}
			if len(payload) < chatwootPageSize {
				break
			}
			// Trang đầy nhưng không thêm hội thoại mới: máy chủ đang bỏ qua
			// tham số page, dừng để tránh lặp vô hạn.
			if added == 0 {
				break
			}
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

func toSyncedConversation(conv chatwootConversation) SyncedConversation {
	meta := map[string]interface{}{
		"inbox_id": conv.InboxID,
		"status":   conv.Status,
	}
	if conv.Meta.Assignee != nil {
		meta["assignee"] = conv.Meta.Assignee.Name
	}
	if conv.Meta.Team != nil {
		meta["team"] = conv.Meta.Team.Name
	}
	return SyncedConversation{
		ExternalID:    strconv.Itoa(conv.DisplayID),
		CustomerName:  conv.Meta.Sender.Name,
		LastMessageAt: time.Unix(conv.LastActivityAt, 0),
		Metadata:      meta,
	}
}

func (c *ChatwootAdapter) HealthCheck(ctx context.Context) error {
	var account struct {
		ID int `json:"id"`
	}
	return c.doJSON(ctx, http.MethodGet, c.url("", nil), &account)
}
