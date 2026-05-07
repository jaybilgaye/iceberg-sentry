package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// S3 is a Storage backend backed by Amazon S3 (or any S3-compatible service
// reachable via aws-sdk-go-v2 — MinIO, LocalStack, etc).
type S3 struct {
	client *s3.Client
	// PathStyle forces path-style addressing (required for MinIO/LocalStack).
	PathStyle bool
}

// S3Option configures the S3 backend.
type S3Option func(*S3)

// WithS3Client injects a pre-built S3 client (used in tests with fakes).
func WithS3Client(c *s3.Client) S3Option {
	return func(s *S3) { s.client = c }
}

// WithS3PathStyle enables path-style addressing (bucket as URL path segment).
func WithS3PathStyle(b bool) S3Option {
	return func(s *S3) { s.PathStyle = b }
}

// NewS3 builds an S3 backend, loading default AWS credentials and region
// unless a client is provided via WithS3Client.
func NewS3(ctx context.Context, opts ...S3Option) (*S3, error) {
	s := &S3{}
	for _, o := range opts {
		o(s)
	}
	if s.client == nil {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		s.client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.UsePathStyle = s.PathStyle
		})
	}
	return s, nil
}

// Scheme returns "s3".
func (s *S3) Scheme() string { return "s3" }

func splitS3URI(uri string) (bucket, key string, err error) {
	scheme, host, path, err := SplitURI(uri)
	if err != nil {
		return "", "", err
	}
	if scheme != "s3" {
		return "", "", fmt.Errorf("not an s3 uri: %q", uri)
	}
	if host == "" {
		return "", "", fmt.Errorf("s3 uri %q has no bucket", uri)
	}
	return host, strings.TrimPrefix(path, "/"), nil
}

// Open returns a streaming reader for the requested S3 object.
func (s *S3) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	bucket, key, err := splitS3URI(uri)
	if err != nil {
		return nil, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound" {
				return nil, fmt.Errorf("%s: %w", uri, ErrNotFound)
			}
		}
		return nil, fmt.Errorf("s3 get %s: %w", uri, err)
	}
	return out.Body, nil
}

// List streams object metadata under the prefix using S3's V2 list API.
func (s *S3) List(ctx context.Context, prefix string, visit func(ObjectInfo) error) error {
	bucket, key, err := splitS3URI(prefix)
	if err != nil {
		return err
	}
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(key),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("s3 list %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			info := ObjectInfo{
				URI:  fmt.Sprintf("s3://%s/%s", bucket, aws.ToString(obj.Key)),
				Size: aws.ToInt64(obj.Size),
			}
			if obj.LastModified != nil {
				info.UpdatedAt = obj.LastModified.UnixMilli()
			}
			if err := visit(info); err != nil {
				return err
			}
		}
	}
	return nil
}
