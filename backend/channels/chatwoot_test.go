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
