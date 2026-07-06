package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
)

func TestLoadTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer abc" {
			t.Errorf("auth header = %q", got)
		}
		switch r.URL.Path {
		case "/v1/namespaces/finance/tables/transactions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"metadata-location": "s3://lake/finance/transactions/metadata/v3.metadata.json",
				"metadata": {"location": "s3://lake/finance/transactions", "properties": {"format-version":"2"}}
			}`))
		case "/v1/namespaces/finance/tables/missing":
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, WithBearerToken("abc"))
	entry, err := c.LoadTable(context.Background(), catalog.TableID{Namespace: "finance", Name: "transactions"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.HasSuffix(entry.MetadataLocation, "v3.metadata.json") {
		t.Errorf("metadata location = %q", entry.MetadataLocation)
	}

	_, err = c.LoadTable(context.Background(), catalog.TableID{Namespace: "finance", Name: "missing"})
	if !errors.Is(err, catalog.ErrTableNotFound) {
		t.Errorf("err = %v, want ErrTableNotFound", err)
	}
}

func TestListTables(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/namespaces/finance/tables" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"identifiers": [
				{"namespace": ["finance"], "name": "transactions"},
				{"namespace": ["finance"], "name": "orders"}
			]
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	got, err := c.ListTables(context.Background(), "finance")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d tables, want 2", len(got))
	}
}
