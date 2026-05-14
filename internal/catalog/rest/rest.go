// Package rest is an Iceberg REST Catalog adapter conforming to the
// open Iceberg REST spec (used by Polaris/Snowflake Open Catalog,
// Tabular, Databricks Unity REST, Nessie REST, and the reference
// Iceberg REST catalog).
//
// Endpoints used by Phase 2:
//
//	GET  /v1/{prefix}/namespaces/{namespace}/tables                 (list)
//	GET  /v1/{prefix}/namespaces/{namespace}/tables/{table}         (load)
//
// Authentication: Phase 2 supports bearer tokens via the Authorization
// header. OAuth2 client-credentials flow is a Phase 3 add (the AuthHeader
// option already lets a caller plug it in).
package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
)

// REST is the adapter.
type REST struct {
	base       string
	client     *http.Client
	prefix     string
	authHeader string

	// OAuth2 client-credentials grant; populated by WithOAuth2ClientCredentials.
	// authHeader is regenerated automatically once the token is fetched.
	oauthMu       sync.Mutex
	oauthTokenURL string
	oauthClientID string
	oauthSecret   string
	oauthScope    string
	oauthToken    string
	oauthExpires  time.Time
}

// Option configures the adapter.
type Option func(*REST)

// WithHTTPClient overrides the HTTP client (e.g. with custom TLS).
func WithHTTPClient(c *http.Client) Option {
	return func(r *REST) { r.client = c }
}

// WithPrefix sets the optional catalog prefix segment ("/{prefix}" in the URL).
func WithPrefix(p string) Option {
	return func(r *REST) { r.prefix = strings.Trim(p, "/") }
}

// WithBearerToken adds an Authorization: Bearer header to every request.
func WithBearerToken(tok string) Option {
	return func(r *REST) {
		if tok != "" {
			r.authHeader = "Bearer " + tok
		}
	}
}

// WithAuthHeader sets an arbitrary Authorization header value.
func WithAuthHeader(h string) Option {
	return func(r *REST) { r.authHeader = h }
}

// WithOAuth2ClientCredentials enables OAuth2 client_credentials grant flow.
// Polaris and Unity REST both follow RFC 6749; the adapter caches the token
// and refreshes it 30s before expiry. Scope is optional.
func WithOAuth2ClientCredentials(tokenURL, clientID, clientSecret string) Option {
	return func(r *REST) {
		r.oauthTokenURL = tokenURL
		r.oauthClientID = clientID
		r.oauthSecret = clientSecret
	}
}

// WithOAuth2Scope sets the OAuth2 scope (default empty).
func WithOAuth2Scope(scope string) Option {
	return func(r *REST) { r.oauthScope = scope }
}

// New builds an Iceberg REST catalog client.
func New(baseURL string, opts ...Option) *REST {
	r := &REST{
		base:   strings.TrimRight(baseURL, "/"),
		client: http.DefaultClient,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Name returns the catalog label.
func (r *REST) Name() string { return "rest:" + r.base }

func (r *REST) urlFor(parts ...string) string {
	segs := []string{r.base, "v1"}
	if r.prefix != "" {
		segs = append(segs, r.prefix)
	}
	for _, p := range parts {
		segs = append(segs, p)
	}
	return strings.Join(segs, "/")
}

func (r *REST) do(ctx context.Context, method, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if r.oauthTokenURL != "" {
		tok, err := r.ensureOAuthToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if r.authHeader != "" {
		req.Header.Set("Authorization", r.authHeader)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return catalog.ErrTableNotFound
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: status %d: %s", method, u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type loadTableResponse struct {
	MetadataLocation string            `json:"metadata-location"`
	Properties       map[string]string `json:"config"`
	Metadata         struct {
		Location   string            `json:"location"`
		Properties map[string]string `json:"properties"`
	} `json:"metadata"`
}

// LoadTable fetches the metadata-location for the table.
func (r *REST) LoadTable(ctx context.Context, id catalog.TableID) (*catalog.TableEntry, error) {
	if id.Namespace == "" || id.Name == "" {
		return nil, errors.New("rest: table id requires namespace and name")
	}
	u := r.urlFor("namespaces", url.PathEscape(id.Namespace), "tables", url.PathEscape(id.Name))
	var resp loadTableResponse
	if err := r.do(ctx, http.MethodGet, u, &resp); err != nil {
		if errors.Is(err, catalog.ErrTableNotFound) {
			return nil, fmt.Errorf("%s: %w", id, catalog.ErrTableNotFound)
		}
		return nil, err
	}
	if resp.MetadataLocation == "" {
		return nil, fmt.Errorf("%s: REST response missing metadata-location", id)
	}
	return &catalog.TableEntry{
		ID:               id,
		MetadataLocation: resp.MetadataLocation,
		Properties:       resp.Metadata.Properties,
	}, nil
}

type listTablesResponse struct {
	Identifiers []struct {
		Namespace []string `json:"namespace"`
		Name      string   `json:"name"`
	} `json:"identifiers"`
	NextPageToken string `json:"next-page-token,omitempty"`
}

// ListTables enumerates tables in a namespace.
func (r *REST) ListTables(ctx context.Context, namespace string) ([]catalog.TableID, error) {
	if namespace == "" {
		return nil, errors.New("rest: namespace is required for listing")
	}
	u := r.urlFor("namespaces", url.PathEscape(namespace), "tables")
	var resp listTablesResponse
	if err := r.do(ctx, http.MethodGet, u, &resp); err != nil {
		return nil, err
	}
	out := make([]catalog.TableID, 0, len(resp.Identifiers))
	for _, i := range resp.Identifiers {
		out = append(out, catalog.TableID{
			Namespace: strings.Join(i.Namespace, "."),
			Name:      i.Name,
		})
	}
	return out, nil
}

// ensureOAuthToken returns a cached bearer token, refreshing 30s before
// expiry. RFC 6749 client_credentials grant.
func (r *REST) ensureOAuthToken(ctx context.Context) (string, error) {
	r.oauthMu.Lock()
	defer r.oauthMu.Unlock()
	if r.oauthToken != "" && time.Until(r.oauthExpires) > 30*time.Second {
		return r.oauthToken, nil
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", r.oauthClientID)
	form.Set("client_secret", r.oauthSecret)
	if r.oauthScope != "" {
		form.Set("scope", r.oauthScope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth2 token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("oauth2 token status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("oauth2 decode: %w", err)
	}
	if tr.AccessToken == "" {
		return "", errors.New("oauth2: empty access_token in response")
	}
	r.oauthToken = tr.AccessToken
	if tr.ExpiresIn > 0 {
		r.oauthExpires = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	} else {
		r.oauthExpires = time.Now().Add(1 * time.Hour)
	}
	return r.oauthToken, nil
}
