package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
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
		return fmt.Errorf("chatwoot %s %s: status %d: %s", method, target, resp.StatusCode, string(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

type chatwootConversation struct {
	ID             int    `json:"id"`
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
	for _, inboxID := range inboxes {
		for page := 1; ; page++ {
			q := url.Values{
				"status":  {"all"},
				"sort_by": {"last_activity_at"},
				"page":    {strconv.Itoa(page)},
			}
			if inboxID > 0 {
				q.Set("inbox_id", strconv.Itoa(inboxID))
			}
			var resp chatwootConversationList
			if err := c.doJSON(ctx, http.MethodGet, c.url("/conversations", q), &resp); err != nil {
				return nil, err
			}
			if len(resp.Data.Payload) == 0 {
				break
			}
			olderFound := false
			for _, conv := range resp.Data.Payload {
				if conv.LastActivityAt > 0 && time.Unix(conv.LastActivityAt, 0).Before(since) {
					olderFound = true
					break
				}
				if seen[conv.ID] {
					continue
				}
				seen[conv.ID] = true
				out = append(out, toSyncedConversation(conv))
				if len(out) >= limit {
					return out, nil
				}
			}
			if olderFound || len(resp.Data.Payload) < 25 {
				break
			}
		}
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
		ExternalID:    strconv.Itoa(conv.ID),
		CustomerName:  conv.Meta.Sender.Name,
		LastMessageAt: time.Unix(conv.LastActivityAt, 0),
		Metadata:      meta,
	}
}

func (c *ChatwootAdapter) HealthCheck(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodGet, c.url("", nil), nil)
}
