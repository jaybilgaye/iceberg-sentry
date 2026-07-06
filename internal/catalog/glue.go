package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	"github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/aws/smithy-go"
)

// glueAPI is the subset of glue.Client we use; isolating it keeps Glue
// adapter tests injectable.
type glueAPI interface {
	GetTable(ctx context.Context, in *glue.GetTableInput, optFns ...func(*glue.Options)) (*glue.GetTableOutput, error)
	GetTables(ctx context.Context, in *glue.GetTablesInput, optFns ...func(*glue.Options)) (*glue.GetTablesOutput, error)
}

// Glue is an AWS Glue Data Catalog adapter. Iceberg tables stored in Glue
// expose their current metadata location via the table parameter
// "metadata_location" (lowercase by convention; uppercase fallback handled).
type Glue struct {
	client    glueAPI
	CatalogID string // optional; cross-account Glue access via STS
}

// GlueOption configures the Glue adapter.
type GlueOption func(*Glue)

// WithGlueClient injects a pre-built Glue client (used by tests).
func WithGlueClient(c glueAPI) GlueOption {
	return func(g *Glue) { g.client = c }
}

// WithGlueCatalogID sets the AWS account ID owning the catalog, for
// cross-account access via STS-assumed credentials.
func WithGlueCatalogID(id string) GlueOption {
	return func(g *Glue) { g.CatalogID = id }
}

// NewGlue builds a Glue catalog adapter, loading default AWS credentials
// unless WithGlueClient is supplied.
func NewGlue(ctx context.Context, opts ...GlueOption) (*Glue, error) {
	g := &Glue{}
	for _, o := range opts {
		o(g)
	}
	if g.client == nil {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		g.client = glue.NewFromConfig(cfg)
	}
	return g, nil
}

// Name returns the catalog label.
func (g *Glue) Name() string {
	if g.CatalogID == "" {
		return "glue"
	}
	return "glue:" + g.CatalogID
}

func (g *Glue) catalogPtr() *string {
	if g.CatalogID == "" {
		return nil
	}
	return aws.String(g.CatalogID)
}

// LoadTable looks up a Glue table and extracts its metadata_location.
func (g *Glue) LoadTable(ctx context.Context, id TableID) (*TableEntry, error) {
	out, err := g.client.GetTable(ctx, &glue.GetTableInput{
		CatalogId:    g.catalogPtr(),
		DatabaseName: aws.String(id.Namespace),
		Name:         aws.String(id.Name),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "EntityNotFoundException" {
			return nil, fmt.Errorf("%s: %w", id, ErrTableNotFound)
		}
		return nil, fmt.Errorf("glue GetTable %s: %w", id, err)
	}
	if out.Table == nil {
		return nil, fmt.Errorf("%s: %w", id, ErrTableNotFound)
	}
	loc, ok := lookupMetadataLocation(out.Table.Parameters)
	if !ok {
		return nil, fmt.Errorf("%s: glue table has no metadata_location parameter (is it an Iceberg table?)", id)
	}
	return &TableEntry{
		ID:               id,
		MetadataLocation: loc,
		Properties:       cloneStringMap(out.Table.Parameters),
	}, nil
}

// ListTables enumerates tables in a Glue database. An empty namespace is
// rejected — Glue requires a database name for GetTables.
func (g *Glue) ListTables(ctx context.Context, namespace string) ([]TableID, error) {
	if namespace == "" {
		return nil, fmt.Errorf("glue catalog requires a non-empty namespace for listing")
	}
	var out []TableID
	var token *string
	for {
		page, err := g.client.GetTables(ctx, &glue.GetTablesInput{
			CatalogId:    g.catalogPtr(),
			DatabaseName: aws.String(namespace),
			NextToken:    token,
		})
		if err != nil {
			return nil, fmt.Errorf("glue GetTables %s: %w", namespace, err)
		}
		for _, t := range page.TableList {
			if !isIcebergTable(t.Parameters) {
				continue
			}
			out = append(out, TableID{Namespace: namespace, Name: aws.ToString(t.Name)})
		}
		if page.NextToken == nil {
			return out, nil
		}
		token = page.NextToken
	}
}

func lookupMetadataLocation(params map[string]string) (string, bool) {
	if params == nil {
		return "", false
	}
	for _, key := range []string{"metadata_location", "metadata-location", "MetadataLocation"} {
		if v, ok := params[key]; ok && v != "" {
			return v, true
		}
	}
	return "", false
}

func isIcebergTable(params map[string]string) bool {
	if params == nil {
		return false
	}
	if v, ok := params["table_type"]; ok && v != "" {
		// Glue convention: ICEBERG (uppercase). Be tolerant.
		return iEqual(v, "ICEBERG")
	}
	_, ok := lookupMetadataLocation(params)
	return ok
}

func iEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// _ usage to silence unused import warnings if AWS types are not referenced in
// this file's tests (types is used for clarity in option signatures elsewhere).
var _ = types.TableInput{}
