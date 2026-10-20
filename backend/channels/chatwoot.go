package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Chatwoot trả 25 hội thoại mỗi trang khi không truyền per_page.
	chatwootPageSize = 25

	// Chatwoot trả 20 tin nhắn mỗi trang, kể cả khi truyền `before`.
	chatwootMessagePageSize = 20

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

	agentMu      sync.Mutex
	agents       map[int]string // chatwoot user id -> email
	agentsLoaded bool
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

type chatwootMessage struct {
	ID          int    `json:"id"`
	MessageType int    `json:"message_type"`
	Private     bool   `json:"private"`
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
	CreatedAt   int64  `json:"created_at"`
	Sender      *struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
		Type  string `json:"type"`
	} `json:"sender"`
	Attachments []struct {
		ID        int    `json:"id"`
		DataURL   string `json:"data_url"`
		FileType  string `json:"file_type"`
		Extension string `json:"extension"`
	} `json:"attachments"`
}

type chatwootMessageList struct {
	Payload []chatwootMessage `json:"payload"`
}

type chatwootAgent struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type chatwootAgentList struct {
	Payload []chatwootAgent `json:"payload"`
}

func (c *ChatwootAdapter) ensureAgents(ctx context.Context) error {
	c.agentMu.Lock()
	if c.agentsLoaded {
		c.agentMu.Unlock()
		return nil
	}
	c.agentMu.Unlock()

	var resp chatwootAgentList
	if err := c.doJSON(ctx, http.MethodGet, c.url("/agents", nil), &resp); err != nil {
		// Negative cache: degrade to bare sender ids for this adapter instead of
		// retrying /agents on every conversation.
		log.Printf("[chatwoot] load agents failed (falling back to sender ids): %v", err)
		c.agentMu.Lock()
		c.agentsLoaded = true
		c.agentMu.Unlock()
		return nil
	}

	c.agentMu.Lock()
	for _, a := range resp.Payload {
		c.agents[a.ID] = a.Email
	}
	c.agentsLoaded = true
	c.agentMu.Unlock()
	return nil
}

func (c *ChatwootAdapter) FetchMessages(ctx context.Context, conversationID string, since time.Time) ([]SyncedMessage, error) {
	c.ensureAgents(ctx)
	before := 0
	out := make([]SyncedMessage, 0, 20)
	for {
		q := url.Values{"filter_internal_messages": {"1"}}
		if before > 0 {
			q.Set("before", strconv.Itoa(before))
		}
		target := c.url("/conversations/"+conversationID+"/messages", q)
		var resp chatwootMessageList
		if err := c.doJSON(ctx, http.MethodGet, target, &resp); err != nil {
			return nil, fmt.Errorf("chatwoot messages %s: %w", conversationID, err)
		}
		if len(resp.Payload) == 0 {
			break
		}
		oldest := int64(0)
		minID := 0
		pageMsgs := make([]SyncedMessage, 0, len(resp.Payload))
		for _, m := range resp.Payload {
			if minID == 0 || m.ID < minID {
				minID = m.ID
			}
			if oldest == 0 || m.CreatedAt < oldest {
				oldest = m.CreatedAt
			}
			if m.MessageType != 0 && m.MessageType != 1 {
				continue
			}
			if m.Private {
				continue
			}
			if m.CreatedAt <= 0 {
				continue
			}
			if time.Unix(m.CreatedAt, 0).Before(since) {
				continue
			}
			pageMsgs = append(pageMsgs, c.toSyncedMessage(m))
		}
		// Chatwoot trả mỗi trang theo thứ tự tăng dần; trang mới hơn đứng trước.
		out = append(pageMsgs, out...)
		if len(resp.Payload) < chatwootMessagePageSize {
			break
		}
		if oldest > 0 && time.Unix(oldest, 0).Before(since) {
			break
		}
		if minID == 0 || minID == before {
			break
		}
		before = minID
	}
	return out, nil
}

func (c *ChatwootAdapter) toSyncedMessage(m chatwootMessage) SyncedMessage {
	msg := SyncedMessage{
		ExternalID:  strconv.Itoa(m.ID),
		SenderType:  "system",
		Content:     m.Content,
		ContentType: m.ContentType,
		SentAt:      time.Unix(m.CreatedAt, 0),
		RawData:     map[string]interface{}{"message_type": m.MessageType},
	}
	if m.Sender != nil {
		msg.SenderExternalID = strconv.Itoa(m.Sender.ID)
		msg.SenderName = m.Sender.Name
		switch m.Sender.Type {
		case "user":
			msg.SenderType = "agent"
			c.agentMu.Lock()
			msg.SenderEmail = c.agents[m.Sender.ID]
			c.agentMu.Unlock()
		case "contact":
			msg.SenderType = "customer"
			msg.SenderEmail = m.Sender.Email
		}
	}
	// Chatwoot `data_url` là URL có chữ ký và hết hạn, nên bước sync file phải
	// tải xuống sớm thay vì lưu URL để dùng lại sau.
	for _, a := range m.Attachments {
		if a.DataURL == "" {
			continue
		}
		kind := "file"
		switch {
		case strings.HasPrefix(a.FileType, "image"):
			kind = "image"
		case strings.HasPrefix(a.FileType, "video"):
			kind = "video"
		}
		msg.Attachments = append(msg.Attachments, Attachment{
			Type: kind,
			URL:  a.DataURL,
			Name: attachmentName(a.ID, a.Extension, a.DataURL),
		})
	}
	return msg
}

// attachmentName tạo tên file duy nhất trong phạm vi hội thoại: mỗi tệp đính
// kèm phải có khóa lưu trữ riêng, nếu chỉ dùng phần mở rộng thì các tệp cùng
// loại sẽ ghi đè lên nhau.
func attachmentName(id int, extension, dataURL string) string {
	if id > 0 {
		if extension != "" {
			return fmt.Sprintf("%d.%s", id, extension)
		}
		return strconv.Itoa(id)
	}
	if u, err := url.Parse(dataURL); err == nil {
		if base := path.Base(u.Path); base != "" && base != "." && base != "/" {
			return base
		}
	}
	return "attachment"
}
