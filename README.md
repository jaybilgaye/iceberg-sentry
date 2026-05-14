# Iceberg Sentry

Iceberg-native lakehouse reliability linter — see
[`Iceberg_Sentry_Spec_v2.md`](./Iceberg_Sentry_Spec_v2.md) for the full
product specification.

This repository contains the working **Phase 1 + Phase 2 + Phase 3** delivery:
an end-to-end CLI that scores Apache Iceberg table health, finds orphan
files, samples for PII, classifies write patterns, runs a Cloudera CDP
Migration Readiness Audit, models snapshot cost trajectories, and exports
Prometheus metrics — all from metadata alone, with no compute engine
required.

## What's implemented

| Capability                  | Status                              |
|-----------------------------|-------------------------------------|
| Iceberg metadata JSON       | v1, v2, v3 (forward-compat parsing) |
| Streaming Avro manifests    | manifest-list and manifest-file     |
| Storage backends            | local FS, S3, WebHDFS               |
| Catalogs                    | LocalFS, AWS Glue, Hive Metastore (Kerberos optional), Iceberg REST (OAuth2 client-credentials) |
| Health dimensions           | file_size, delete_amplification, manifest_density, snapshot, partition_skew, format_version |
| Write-pattern classifier    | streaming / batch / mixed / unknown |
| Branch/tag-aware scanning   | `--branch <name>` flag              |
| Orphan-file discovery       | Bloom-filter + storage crawl, dry-run, grace period |
| PII scanner                 | Parquet row-group sampling, regex + entropy, Atlas-compatible JSON |
| `bench` start/compare       | persisted baseline + before/after diff |
| `migration` audit           | HDFS→CDP risk score, HDFS-path/property detection, v1→v2 hint |
| `cost` timeline             | Pluggable cost provider, snapshot-history $/mo, cold-tier flags |
| `export` Prometheus server  | `/metrics` + `/healthz` for ServiceMonitor scraping |
| Push-gateway delivery       | `audit --format prometheus --push-gateway URL` |
| Atlas/UC bulk-import payload | both PII and health-side findings   |
| CLI exit codes              | 0–5 per spec §3.1                   |
| Output formats              | `text`, `json`, `sarif`, `prometheus` |
| Policy-as-code              | `sentry.yaml`                       |
| GitHub Actions example      | SARIF upload + PR comment workflow  |
| Perf regression CI          | benchstat over `internal/scan` and `internal/iceberg` |
| Integration harness         | docker-compose for MinIO + REST + HMS |
| Helm chart                  | CronJob + Deployment + ServiceMonitor under `deploy/helm/` |
| K8s manifests               | `deploy/k8s/` standalone YAMLs       |
| Grafana dashboard           | `deploy/grafana-dashboard.json`      |
| Tests                       | unit + end-to-end pyiceberg-generated fixtures |

Phase 4 items (Nessie branch-history, Slack/PagerDuty alerts, dbt
integration, OpenMetadata/DataHub exporter plugins, hosted SaaS MVP)
remain on the roadmap.

## Build

```
go build -o iceberg-sentry ./cmd/iceberg-sentry
```

## Usage

Audit a single table from a local filesystem catalog:

```
iceberg-sentry audit \
  --catalog localfs \
  --catalog-root /path/to/warehouse \
  --table finance.transactions \
  --format text
```

Audit a Glue namespace and emit JSON for downstream processing:

```
iceberg-sentry audit \
  --catalog glue \
  --namespace finance \
  --format json
```

Apply a policy file and fail CI on warnings:

```
iceberg-sentry audit \
  --catalog localfs --catalog-root ./warehouse \
  --namespace finance \
  --policy ./examples/sentry.yaml \
  --fail-on warn
```

Emit SARIF for GitHub Code Scanning:

```
iceberg-sentry audit \
  --catalog rest --rest http://localhost:8181 \
  --namespace finance \
  --format sarif > iceberg.sarif
```

Find orphan files (dry-run, 6-hour grace window):

```
iceberg-sentry orphans \
  --catalog rest --rest http://localhost:8181 \
  --table finance.transactions \
  --grace-period 6h
```

Sample for PII and write an Atlas-importable JSON payload:

```
iceberg-sentry pii \
  --catalog glue --namespace finance \
  --table finance.transactions \
  --atlas-output atlas-import-$(date +%Y%m%d).json
```

Capture a baseline, run compaction, then compare:

```
iceberg-sentry bench start   --catalog rest --rest http://localhost:8181 --table finance.transactions --tag pre-compact
# run your CALL ... rewrite_data_files
iceberg-sentry bench compare --catalog rest --rest http://localhost:8181 --table finance.transactions --tag pre-compact
```

Run the Migration Readiness Audit:

```
iceberg-sentry migration \
  --catalog hive --hive hms.onprem:9083 \
  --namespace finance
```

Snapshot Cost Timeline (cold-tier candidates flagged):

```
iceberg-sentry cost \
  --catalog rest --rest http://localhost:8181 \
  --table finance.transactions \
  --cold-tier-days 90
```

Push metrics to Prometheus from a CronJob:

```
iceberg-sentry audit \
  --catalog glue --namespace finance \
  --format prometheus \
  --push-gateway http://prometheus-pushgateway:9091 \
  --push-job iceberg-sentry
```

Long-lived scrape exporter (ServiceMonitor-compatible):

```
iceberg-sentry export \
  --catalog glue --namespace finance \
  --listen :9400 --interval 5m
```

Polaris / Databricks Unity REST via OAuth2 client-credentials:

```
iceberg-sentry audit \
  --catalog rest --rest https://polaris.example.com/api/catalog \
  --rest-oauth-client-id "$CLIENT_ID" \
  --rest-oauth-client-secret "$CLIENT_SECRET" \
  --rest-oauth-token-url https://polaris.example.com/api/oauth/token \
  --namespace finance
```

Hive Metastore with Kerberos:

```
iceberg-sentry audit \
  --catalog hive --hive hms.example.com:9083 \
  --hive-principal "iceberg-sentry@EXAMPLE.COM" \
  --hive-keytab /etc/iceberg-sentry/sentry.keytab \
  --namespace finance
```

## Development

Run the full suite:

```
go test -race ./...
```

Generate richer integration fixtures with pyiceberg:

```
pip install -r scripts/requirements.txt
python scripts/gen_fixtures.py --root testdata/fixtures/generated
```

## Repository layout

```
cmd/iceberg-sentry/    CLI entry point
internal/
  cli/                 cobra commands (audit, orphans, pii, bench, migration, cost, export, version)
  iceberg/             metadata JSON + Avro manifest parsers
  storage/             local, S3, WebHDFS storage backends
  catalog/             LocalFS, Glue, Hive (Thrift+Kerberos), REST (OAuth2) adapters
  scan/                orchestration: catalog → metadata → manifests → stats
  health/              composite score + per-dimension diagnostics
  bloom/               double-hashed Bloom filter for orphan tracking
  writepattern/        streaming/batch classifier
  orphans/             differential storage-vs-metadata scan
  pii/                 Parquet row-group sampler + regex/entropy detector
  migration/           HDFS → CDP Migration Readiness Audit
  cost/                pluggable cost provider + snapshot cost timeline
  metrics/             Prometheus exposition format + push gateway client
  output/              text, json, SARIF, prometheus renderers
  policy/              sentry.yaml parser and threshold application
  exitcode/            shared exit-code constants
examples/
  sentry.yaml                       sample policy
  github-actions/iceberg-health.yml SARIF + PR-comment workflow
deploy/
  k8s/                              raw CronJob + Deployment + ServiceMonitor
  helm/                             Helm chart (Chart.yaml, values.yaml, templates/)
  grafana-dashboard.json            ready-to-import dashboard
scripts/gen_fixtures.py             pyiceberg integration-fixture generator
testdata/integration/               docker-compose harness (MinIO + REST + HMS)
```

## License

Apache 2.0 — see [LICENSE](./LICENSE).
