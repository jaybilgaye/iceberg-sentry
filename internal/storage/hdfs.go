package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HDFS is a Storage backend that talks to a NameNode's WebHDFS REST API. It
// is intentionally lightweight — full RPC HDFS clients exist (colinmarc/hdfs)
// but require Kerberos/SASL handling that belongs in a dedicated module.
//
// WebHDFS endpoints follow the convention:
//
//	http(s)://<host>:<port>/webhdfs/v1/<path>?op=OPEN
type HDFS struct {
	endpoint string // e.g. https://namenode:14000/webhdfs/v1
	client   *http.Client
	// User overrides the WebHDFS user.name parameter (no-auth clusters).
	User string
}

// HDFSOption configures the HDFS backend.
type HDFSOption func(*HDFS)

// WithHDFSClient injects a custom HTTP client (proxy, mTLS, Kerberos transport).
func WithHDFSClient(c *http.Client) HDFSOption {
	return func(h *HDFS) { h.client = c }
}

// WithHDFSUser sets the WebHDFS user.name parameter for simple auth clusters.
func WithHDFSUser(u string) HDFSOption {
	return func(h *HDFS) { h.User = u }
}

// NewHDFS builds an HDFS WebHDFS-backed storage backend. The endpoint must
// be the WebHDFS root, e.g. https://namenode:14000/webhdfs/v1
func NewHDFS(endpoint string, opts ...HDFSOption) *HDFS {
	h := &HDFS{
		endpoint: strings.TrimRight(endpoint, "/"),
		client:   http.DefaultClient,
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Scheme returns "hdfs".
func (h *HDFS) Scheme() string { return "hdfs" }

func (h *HDFS) urlFor(uri, op string) (string, error) {
	_, _, path, err := SplitURI(uri)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("op", op)
	if h.User != "" {
		q.Set("user.name", h.User)
	}
	return fmt.Sprintf("%s%s?%s", h.endpoint, path, q.Encode()), nil
}

// Open issues a WebHDFS OPEN request and returns the streaming body.
func (h *HDFS) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	u, err := h.urlFor(uri, "OPEN")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webhdfs open %s: %w", uri, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: %w", uri, ErrNotFound)
	}
	if resp.StatusCode >= 400 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("webhdfs open %s: status %d", uri, resp.StatusCode)
	}
	return resp.Body, nil
}

// List uses WebHDFS LISTSTATUS_BATCH to stream directory entries (recursive).
func (h *HDFS) List(ctx context.Context, prefix string, visit func(ObjectInfo) error) error {
	return h.list(ctx, prefix, prefix, visit)
}

func (h *HDFS) list(ctx context.Context, root, current string, visit func(ObjectInfo) error) error {
	u, err := h.urlFor(current, "LISTSTATUS")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhdfs list %s: %w", current, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s: %w", current, ErrNotFound)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhdfs list %s: status %d", current, resp.StatusCode)
	}
	var body struct {
		FileStatuses struct {
			FileStatus []struct {
				PathSuffix       string `json:"pathSuffix"`
				Type             string `json:"type"`
				Length           int64  `json:"length"`
				ModificationTime int64  `json:"modificationTime"`
			} `json:"FileStatus"`
		} `json:"FileStatuses"`
		RemoteException *struct {
			Exception string `json:"exception"`
			Message   string `json:"message"`
		} `json:"RemoteException,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode webhdfs list response: %w", err)
	}
	if body.RemoteException != nil {
		return errors.New(body.RemoteException.Message)
	}
	for _, fs := range body.FileStatuses.FileStatus {
		child := strings.TrimRight(current, "/") + "/" + fs.PathSuffix
		if fs.Type == "DIRECTORY" {
			if err := h.list(ctx, root, child, visit); err != nil {
				return err
			}
			continue
		}
		if err := visit(ObjectInfo{
			URI:       child,
			Size:      fs.Length,
			UpdatedAt: fs.ModificationTime,
		}); err != nil {
			return err
		}
	}
	return nil
}
