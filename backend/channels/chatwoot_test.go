package channels

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatwootFetchRecentConversations(t *testing.T) {
	var gotInbox, gotStatus, gotSort string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api_access_token") != "token-1" {
			t.Errorf("missing api_access_token header")
		}
		gotInbox = r.URL.Query().Get("inbox_id")
		gotStatus = r.URL.Query().Get("status")
		gotSort = r.URL.Query().Get("sort_by")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"payload":[
			{"id":101,"last_activity_at":1700000200,"inbox_id":7,"status":"open",
			 "meta":{"sender":{"name":"Khach A"},"assignee":{"name":"Nhan vien A"},"team":{"name":"Sales"}}},
			{"id":102,"last_activity_at":1600000000,"inbox_id":7,"status":"resolved",
			 "meta":{"sender":{"name":"Khach B"}}}
		]}}`)
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{
		BaseURL: srv.URL, AccountID: 1, APIToken: "token-1", InboxIDs: []int{7},
	})
	since := time.Unix(1650000000, 0)
	got, err := adapter.FetchRecentConversations(context.Background(), since, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotInbox != "7" || gotStatus != "all" || gotSort != "last_activity_at_desc" {
		t.Fatalf("query = inbox:%q status:%q sort:%q", gotInbox, gotStatus, gotSort)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 conversation newer than since, got %d", len(got))
	}
	if got[0].ExternalID != "101" || got[0].CustomerName != "Khach A" {
		t.Fatalf("unexpected conversation: %+v", got[0])
	}
	if got[0].Metadata["inbox_id"] != float64(7) && got[0].Metadata["inbox_id"] != 7 {
		t.Fatalf("missing inbox metadata: %+v", got[0].Metadata)
	}
	if !got[0].LastMessageAt.Equal(time.Unix(1700000200, 0)) {
		t.Fatalf("LastMessageAt = %v, want %v", got[0].LastMessageAt, time.Unix(1700000200, 0))
	}
	if got[0].Metadata["status"] != "open" {
		t.Fatalf("status metadata = %v, want open", got[0].Metadata["status"])
	}
}

func TestChatwootPagination(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		w.Header().Set("Content-Type", "application/json")
		if page == "2" {
			fmt.Fprint(w, `{"data":{"payload":[
				{"id":900,"last_activity_at":1600000000,"inbox_id":7,"status":"open","meta":{"sender":{"name":"Khach cu"}}}
			]}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"payload":[`)
		for i := 0; i < 25; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%d,"last_activity_at":%d,"inbox_id":7,"status":"open","meta":{"sender":{"name":"Khach %d"}}}`,
				100+i, 1700000000+i, i)
		}
		fmt.Fprint(w, `]}}`)
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{
		BaseURL: srv.URL, AccountID: 1, APIToken: "token-1", InboxIDs: []int{7},
	})
	got, err := adapter.FetchRecentConversations(context.Background(), time.Unix(1650000000, 0), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 25 {
		t.Fatalf("want 25 conversations, got %d", len(got))
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("pages requested = %v, want [1 2]", pages)
	}
}

func TestChatwootLimitPerInbox(t *testing.T) {
	observed := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inbox := r.URL.Query().Get("inbox_id")
		observed[inbox] = true
		id := 100
		if inbox == "8" {
			id = 200
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"payload":[
			{"id":%d,"last_activity_at":1700000100,"inbox_id":%s,"status":"open","meta":{"sender":{"name":"Khach %s"}}},
			{"id":%d,"last_activity_at":1700000050,"inbox_id":%s,"status":"open","meta":{"sender":{"name":"Khach %s"}}}
		]}}`, id, inbox, inbox, id+1, inbox, inbox)
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{
		BaseURL: srv.URL, AccountID: 1, APIToken: "token-1", InboxIDs: []int{7, 8},
	})
	got, err := adapter.FetchRecentConversations(context.Background(), time.Unix(1650000000, 0), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 conversations (one per inbox), got %d", len(got))
	}
	if !observed["7"] || !observed["8"] {
		t.Fatalf("inboxes observed = %v, want 7 and 8", observed)
	}
}

func TestChatwootSkipsZeroTimestamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"payload":[
			{"id":200,"last_activity_at":0,"inbox_id":7,"status":"open","meta":{"sender":{"name":"Khong co moc"}}},
			{"id":201,"last_activity_at":1700000200,"inbox_id":7,"status":"open","meta":{"sender":{"name":"Khach B"}}}
		]}}`)
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{
		BaseURL: srv.URL, AccountID: 1, APIToken: "token-1", InboxIDs: []int{7},
	})
	got, err := adapter.FetchRecentConversations(context.Background(), time.Unix(1650000000, 0), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ExternalID != "201" {
		t.Fatalf("want only conversation 201, got %+v", got)
	}
}

func TestChatwootPartialInboxFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("inbox_id") == "7" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"payload":[
			{"id":301,"last_activity_at":1700000200,"inbox_id":8,"status":"open","meta":{"sender":{"name":"Khach C"}}}
		]}}`)
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{
		BaseURL: srv.URL, AccountID: 1, APIToken: "token-1", InboxIDs: []int{7, 8},
	})
	got, err := adapter.FetchRecentConversations(context.Background(), time.Unix(1650000000, 0), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ExternalID != "301" {
		t.Fatalf("want conversation 301 from healthy inbox, got %+v", got)
	}
}

func TestChatwootHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/accounts/1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":1}`)
	}))
	defer srv.Close()
	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
	if err := adapter.HealthCheck(context.Background()); err != nil {
		t.Fatalf("health check failed: %v", err)
	}
}

func TestChatwootHealthCheckFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized"}`)
	}))
	defer srv.Close()
	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "bad"})
	if err := adapter.HealthCheck(context.Background()); err == nil {
		t.Fatal("want error for 401 response, got nil")
	}
}

func TestChatwootFetchMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts/1/agents":
			fmt.Fprint(w, `{"payload":[{"id":9,"name":"Nhan vien A","email":"a@dps.media"}]}`)
		case "/api/v1/accounts/1/conversations/101/messages":
			if r.URL.Query().Get("filter_internal_messages") != "1" {
				t.Errorf("missing filter_internal_messages")
			}
			fmt.Fprint(w, `{"payload":[
				{"id":501,"message_type":0,"private":false,"content":"Em chao anh","content_type":"text","created_at":1700000000,
				 "sender":{"id":77,"name":"Khach A","type":"contact"}},
				{"id":502,"message_type":1,"private":false,"content":"Da em nghe","content_type":"text","created_at":1700000100,
				 "sender":{"id":9,"name":"Nhan vien A","type":"user"}},
				{"id":503,"message_type":2,"private":false,"content":"assigned","content_type":"text","created_at":1700000200,
				 "sender":{"id":1,"name":"System","type":"user"}},
				{"id":504,"message_type":1,"private":true,"content":"note rieng","content_type":"text","created_at":1700000300,
				 "sender":{"id":9,"name":"Nhan vien A","type":"user"}}
			]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
	msgs, err := adapter.FetchMessages(context.Background(), "101", time.Unix(1650000000, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 kept messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].SenderExternalID != "77" || msgs[0].SenderType != "customer" {
		t.Fatalf("bad customer map: %+v", msgs[0])
	}
	if msgs[1].SenderExternalID != "9" || msgs[1].SenderType != "agent" || msgs[1].SenderEmail != "a@dps.media" {
		t.Fatalf("bad agent map: %+v", msgs[1])
	}
	if msgs[0].ExternalID != "501" || msgs[1].ExternalID != "502" {
		t.Fatalf("messages not chronological: %+v", msgs)
	}
	if msgs[0].Content != "Em chao anh" || msgs[0].ContentType != "text" || msgs[0].SenderName != "Khach A" {
		t.Fatalf("bad customer content/sender fields: %+v", msgs[0])
	}
}

func TestChatwootFetchMessagesAgentResponseShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"top-level array", ` [ {"id":9,"name":"Agent A","email":"a@dps.media"},{"id":10,"name":"Agent B","email":"b@dps.media"} ] `},
		{"payload envelope", `{"payload":[{"id":9,"name":"Agent A","email":"a@dps.media"},{"id":10,"name":"Agent B","email":"b@dps.media"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/accounts/1/agents":
					agentCalls++
					fmt.Fprint(w, tc.body)
				case "/api/v1/accounts/1/conversations/101/messages", "/api/v1/accounts/1/conversations/102/messages":
					fmt.Fprint(w, `{"payload":[
						{"id":501,"message_type":1,"content":"First reply","created_at":1700000100,"sender":{"id":9,"name":"Agent A","type":"user"}},
						{"id":502,"message_type":1,"content":"Second reply","created_at":1700000200,"sender":{"id":10,"name":"Agent B","type":"user"}}
					]}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer srv.Close()

			adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
			for _, conversationID := range []string{"101", "102"} {
				msgs, err := adapter.FetchMessages(context.Background(), conversationID, time.Unix(1650000000, 0))
				if err != nil {
					t.Fatalf("FetchMessages: %v", err)
				}
				if len(msgs) != 2 {
					t.Fatalf("want 2 messages, got %+v", msgs)
				}
				for i, email := range []string{"a@dps.media", "b@dps.media"} {
					if msgs[i].SenderEmail != email || msgs[i].SenderExternalID != fmt.Sprint(9+i) || msgs[i].SenderType != "agent" {
						t.Errorf("message %d: want mapped agent %s, got %+v", i, email, msgs[i])
					}
				}
			}
			if agentCalls != 1 {
				t.Fatalf("/agents requests = %d, want 1 across conversations", agentCalls)
			}
		})
	}
}

func TestChatwootFetchMessagesAgentFailureNegativeCache(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"HTTP failure", http.StatusInternalServerError, `{"error":"boom"}`},
		{"invalid JSON", http.StatusOK, `[{`},
		{"invalid array field", http.StatusOK, `[{"id":"invalid","email":"a@dps.media"}]`},
		{"invalid envelope field", http.StatusOK, `{"payload":[{"id":"invalid","email":"a@dps.media"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/accounts/1/agents" {
					agentCalls++
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
					return
				}
				fmt.Fprint(w, `{"payload":[{"id":502,"message_type":1,"content":"Reply","created_at":1700000100,"sender":{"id":9,"name":"Agent A","type":"user"}}]}`)
			}))
			defer srv.Close()

			adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
			for _, conversationID := range []string{"101", "102"} {
				msgs, err := adapter.FetchMessages(context.Background(), conversationID, time.Unix(1650000000, 0))
				if err != nil {
					t.Fatalf("FetchMessages should degrade gracefully: %v", err)
				}
				if len(msgs) != 1 || msgs[0].SenderEmail != "" || msgs[0].SenderExternalID != "9" || msgs[0].SenderType != "agent" {
					t.Fatalf("want bare agent identity after lookup failure, got %+v", msgs)
				}
			}
			if agentCalls != 1 {
				t.Fatalf("/agents requests = %d, want 1 after failure", agentCalls)
			}
		})
	}
}

func TestChatwootFetchMessagesPaginates(t *testing.T) {
	var befores []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts/1/agents":
			fmt.Fprint(w, `{"payload":[]}`)
		case "/api/v1/accounts/1/conversations/101/messages":
			before := r.URL.Query().Get("before")
			befores = append(befores, before)
			fmt.Fprint(w, `{"payload":[`)
			if before == "" {
				for i := 501; i <= 520; i++ {
					if i > 501 {
						fmt.Fprint(w, ",")
					}
					fmt.Fprintf(w, `{"id":%d,"message_type":0,"private":false,"content":"m%d","content_type":"text","created_at":%d,"sender":{"id":77,"name":"Khach A","type":"contact"}}`,
						i, i, 1700000000+i)
				}
			} else {
				fmt.Fprint(w, `{"id":500,"message_type":0,"private":false,"content":"m500","content_type":"text","created_at":1699999500,"sender":{"id":77,"name":"Khach A","type":"contact"}}`)
			}
			fmt.Fprint(w, `]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
	msgs, err := adapter.FetchMessages(context.Background(), "101", time.Unix(1650000000, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(befores) != 2 || befores[0] != "" || befores[1] != "501" {
		t.Fatalf("requests before = %v, want [\"\" \"501\"]", befores)
	}
	if len(msgs) != 21 {
		t.Fatalf("want 21 messages, got %d", len(msgs))
	}
	if msgs[0].ExternalID != "500" {
		t.Fatalf("first message = %s, want 500", msgs[0].ExternalID)
	}
	if msgs[len(msgs)-1].ExternalID != "520" {
		t.Fatalf("last message = %s, want 520", msgs[len(msgs)-1].ExternalID)
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i-1].SentAt.After(msgs[i].SentAt) {
			t.Fatalf("messages not ascending at %d: %v after %v", i, msgs[i-1].SentAt, msgs[i].SentAt)
		}
	}
}

func TestChatwootFetchMessagesAgentLookupFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts/1/agents":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
		case "/api/v1/accounts/1/conversations/101/messages":
			fmt.Fprint(w, `{"payload":[
				{"id":502,"message_type":1,"private":false,"content":"Da em nghe","content_type":"text","created_at":1700000100,
				 "sender":{"id":9,"name":"Nhan vien A","type":"user"}}
			]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
	msgs, err := adapter.FetchMessages(context.Background(), "101", time.Unix(1650000000, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if msgs[0].SenderEmail != "" {
		t.Fatalf("SenderEmail = %q, want empty", msgs[0].SenderEmail)
	}
	if msgs[0].SenderType != "agent" {
		t.Fatalf("SenderType = %q, want agent", msgs[0].SenderType)
	}
}

func TestChatwootFetchMessagesAttachments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts/1/agents":
			fmt.Fprint(w, `{"payload":[]}`)
		case "/api/v1/accounts/1/conversations/101/messages":
			fmt.Fprint(w, `{"payload":[
				{"id":600,"message_type":0,"private":false,"content":"files","content_type":"text","created_at":1700000000,
				 "sender":{"id":77,"name":"Khach A","type":"contact"},
				 "attachments":[
					{"id":11,"data_url":"https://cdn.example/a.png","file_type":"image/png","extension":"png"},
					{"id":12,"data_url":"https://cdn.example/b.mp4","file_type":"video/mp4","extension":"mp4"},
					{"id":13,"data_url":"https://cdn.example/c.pdf","file_type":"application/pdf","extension":"pdf"},
					{"id":14,"data_url":"","file_type":"image/png","extension":"png"}
				 ]}
			]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
	msgs, err := adapter.FetchMessages(context.Background(), "101", time.Unix(1650000000, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	atts := msgs[0].Attachments
	if len(atts) != 3 {
		t.Fatalf("want 3 attachments (empty URL skipped), got %d: %+v", len(atts), atts)
	}
	wantTypes := []string{"image", "video", "file"}
	wantNames := []string{"11.png", "12.mp4", "13.pdf"}
	for i, att := range atts {
		if att.Type != wantTypes[i] {
			t.Errorf("attachment %d type = %q, want %q", i, att.Type, wantTypes[i])
		}
		if att.Name != wantNames[i] {
			t.Errorf("attachment %d name = %q, want %q", i, att.Name, wantNames[i])
		}
		if att.URL == "" {
			t.Errorf("attachment %d has empty URL", i)
		}
	}
}

func TestChatwootFetchMessagesAttachmentNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts/1/agents":
			fmt.Fprint(w, `{"payload":[]}`)
		case "/api/v1/accounts/1/conversations/101/messages":
			fmt.Fprint(w, `{"payload":[
				{"id":601,"message_type":0,"private":false,"content":"two images","content_type":"text","created_at":1700000000,
				 "sender":{"id":77,"name":"Khach A","type":"contact"},
				 "attachments":[
					{"id":21,"data_url":"https://cdn.example/x.png","file_type":"image/png","extension":"png"},
					{"id":22,"data_url":"https://cdn.example/y.png","file_type":"image/png","extension":"png"}
				 ]}
			]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
	msgs, err := adapter.FetchMessages(context.Background(), "101", time.Unix(1650000000, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 || len(msgs[0].Attachments) != 2 {
		t.Fatalf("want 2 attachments, got %+v", msgs)
	}
	a, b := msgs[0].Attachments[0], msgs[0].Attachments[1]
	if a.Name == "" || b.Name == "" || a.Name == b.Name {
		t.Fatalf("attachment names must be non-empty and distinct: %q vs %q", a.Name, b.Name)
	}
}

func TestChatwootFetchMessagesSkipsZeroCreatedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts/1/agents":
			fmt.Fprint(w, `{"payload":[]}`)
		case "/api/v1/accounts/1/conversations/101/messages":
			fmt.Fprint(w, `{"payload":[
				{"id":701,"message_type":0,"private":false,"content":"no timestamp","content_type":"text","created_at":0,
				 "sender":{"id":77,"name":"Khach A","type":"contact"}},
				{"id":702,"message_type":0,"private":false,"content":"ok","content_type":"text","created_at":1700000000,
				 "sender":{"id":77,"name":"Khach A","type":"contact"}}
			]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
	msgs, err := adapter.FetchMessages(context.Background(), "101", time.Unix(1650000000, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ExternalID != "702" {
		t.Fatalf("want only message 702, got %+v", msgs)
	}
	if msgs[0].SentAt.Unix() <= 0 {
		t.Fatalf("SentAt = %v, want a real timestamp", msgs[0].SentAt)
	}
}

func TestChatwootFetchMessagesSinceBoundary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts/1/agents":
			fmt.Fprint(w, `{"payload":[]}`)
		case "/api/v1/accounts/1/conversations/101/messages":
			fmt.Fprint(w, `{"payload":[
				{"id":801,"message_type":0,"private":false,"content":"before","content_type":"text","created_at":1699999999,
				 "sender":{"id":77,"name":"Khach A","type":"contact"}},
				{"id":802,"message_type":0,"private":false,"content":"on boundary","content_type":"text","created_at":1700000000,
				 "sender":{"id":77,"name":"Khach A","type":"contact"}},
				{"id":803,"message_type":0,"private":false,"content":"after","content_type":"text","created_at":1700000001,
				 "sender":{"id":77,"name":"Khach A","type":"contact"}}
			]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
	msgs, err := adapter.FetchMessages(context.Background(), "101", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages (inclusive boundary), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].ExternalID != "802" || msgs[1].ExternalID != "803" {
		t.Fatalf("kept messages = %s,%s, want 802,803", msgs[0].ExternalID, msgs[1].ExternalID)
	}
}

func TestChatwootFetchMessagesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts/1/agents":
			fmt.Fprint(w, `{"payload":[]}`)
		case "/api/v1/accounts/1/conversations/101/messages":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	adapter := NewChatwootAdapter(ChatwootCredentials{BaseURL: srv.URL, AccountID: 1, APIToken: "t"})
	msgs, err := adapter.FetchMessages(context.Background(), "101", time.Unix(1650000000, 0))
	if err == nil {
		t.Fatalf("want error, got messages %+v", msgs)
	}
	if !strings.Contains(err.Error(), "101") {
		t.Fatalf("error %q does not mention conversation id", err)
	}
}
