package channels

import (
	"testing"
)

func TestNewAdapterZaloOA(t *testing.T) {
	creds := `{"app_id":"123","app_secret":"abc","access_token":"tok","refresh_token":"ref"}`
	adapter, err := NewAdapter("zalo_oa", []byte(creds))
	if err != nil {
		t.Fatalf("NewAdapter zalo_oa failed: %v", err)
	}
	if adapter == nil {
		t.Fatal("Adapter should not be nil")
	}
}

func TestNewAdapterFacebook(t *testing.T) {
	creds := `{"page_id":"123","access_token":"tok"}`
	adapter, err := NewAdapter("facebook", []byte(creds))
	if err != nil {
		t.Fatalf("NewAdapter facebook failed: %v", err)
	}
	if adapter == nil {
		t.Fatal("Adapter should not be nil")
	}
}

func TestNewAdapterChatwoot(t *testing.T) {
	adapter, err := NewAdapter("chatwoot", []byte(`{"base_url":"http://chatwoot-web:3000","account_id":1,"api_token":"t"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := adapter.(*ChatwootAdapter); !ok {
		t.Fatalf("want *ChatwootAdapter, got %T", adapter)
	}
}

func TestNewAdapterUnsupported(t *testing.T) {
	_, err := NewAdapter("whatsapp", []byte("{}"))
	if err == nil {
		t.Fatal("Should fail for unsupported channel type")
	}
}

func TestNewAdapterInvalidJSON(t *testing.T) {
	_, err := NewAdapter("zalo_oa", []byte("not json"))
	if err == nil {
		t.Fatal("Should fail for invalid JSON")
	}
}
