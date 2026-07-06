package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
)

func TestOAuth2ClientCredentialsCachesToken(t *testing.T) {
	var tokenCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			tokenCalls.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.PostForm.Get("client_id") != "id" || r.PostForm.Get("client_secret") != "sek" {
				t.Errorf("bad form: %v", r.PostForm)
			}
			if r.PostForm.Get("grant_type") != "client_credentials" {
				t.Errorf("grant_type = %q", r.PostForm.Get("grant_type"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok-xyz","token_type":"Bearer","expires_in":3600}`))
		case "/v1/namespaces/finance/tables/transactions":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok-xyz") {
				t.Errorf("auth header = %q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"metadata-location":"s3://lake/x/v.json","metadata":{}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(srv.URL,
		WithOAuth2ClientCredentials(srv.URL+"/oauth/token", "id", "sek"),
	)

	// Two consecutive loads should fetch the token once and reuse it.
	for i := 0; i < 2; i++ {
		_, err := c.LoadTable(context.Background(), catalog.TableID{Namespace: "finance", Name: "transactions"})
		if err != nil {
			t.Fatalf("load #%d: %v", i, err)
		}
	}
	if n := tokenCalls.Load(); n != 1 {
		t.Errorf("oauth token endpoint called %d times, want 1", n)
	}
}
