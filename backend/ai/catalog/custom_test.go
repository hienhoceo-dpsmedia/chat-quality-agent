package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchCustom(t *testing.T) {
	for _, suffix := range []string{"/v1/", "/v1/chat/completions/"} {
		for _, key := range []string{"", "secret"} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RequestURI() != "/v1/models?route=a%2Fb" {
					t.Errorf("wrong target: %s", r.URL)
				}
				want := ""
				if key != "" {
					want = "Bearer " + key
				}
				if r.Header.Get("Authorization") != want {
					t.Error("wrong authorization")
				}
				if key == "" && len(r.Header.Values("Authorization")) != 0 {
					t.Error("keyless header must be absent")
				}
				_, _ = w.Write([]byte(`{"data":[{"id":"vendor/model:latest"},{"id":"gpt-6"},{"id":"text-embedding-3"},{"id":"bad id"}]}`))
			}))
			list, err := FetchCustom(context.Background(), key, srv.URL+suffix+"?route=a%2Fb")
			srv.Close()
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 2 || list[0].ID != "vendor/model:latest" || list[1].ID != "gpt-6" {
				t.Fatalf("unexpected models: %+v", list)
			}
		}
	}
	if _, err := FetchCustom(context.Background(), "", ""); err == nil {
		t.Error("accepted empty endpoint")
	}
}
