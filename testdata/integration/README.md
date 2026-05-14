# Integration test harness

Spin up MinIO + Iceberg REST + Hive Metastore for end-to-end testing:

```
docker compose -f testdata/integration/docker-compose.yml up -d
```

Then load sample tables and exercise the CLI:

```
# Use pyiceberg to write a real v2 table into MinIO + REST
SENTRY_CATALOG_URL=http://localhost:8181 \
SENTRY_S3_ENDPOINT=http://localhost:9000 \
python scripts/gen_fixtures.py --target rest

# Audit it
./iceberg-sentry audit \
  --catalog rest --rest http://localhost:8181 \
  --table fixtures.delete_amp_v2 \
  --s3-path-style \
  --format text

# Find orphans (dry-run, default 24h grace)
./iceberg-sentry orphans \
  --catalog rest --rest http://localhost:8181 \
  --table fixtures.delete_amp_v2 \
  --s3-path-style

# Hive Metastore variant
./iceberg-sentry audit \
  --catalog hive --hive localhost:9083 \
  --table fixtures.delete_amp_v2 \
  --s3-path-style
```

Tear down:

```
docker compose -f testdata/integration/docker-compose.yml down -v
```

Go tests gated on the compose stack are tagged with `//go:build integration`
and run only with `go test -tags=integration ./...`.
