package channels

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
