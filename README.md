# Iceberg Sentry

Iceberg-native lakehouse reliability linter — see
[`Iceberg_Sentry_Spec_v2.md`](./Iceberg_Sentry_Spec_v2.md) for the full
product specification.

This repository contains the working **Phase 1 + Phase 2** delivery: an
end-to-end CLI that scores Apache Iceberg table health, finds orphan files,
samples for PII, classifies write patterns, and produces SARIF-formatted
output for CI gating — all from metadata alone, with no compute engine
required.

## What's implemented

| Capability                 | Status                              |
|----------------------------|-------------------------------------|
| Iceberg metadata JSON      | v1, v2, v3 (forward-compat parsing) |
| Streaming Avro manifests   | manifest-list and manifest-file     |
| Storage backends           | local FS, S3, WebHDFS               |
| Catalogs                   | LocalFS, AWS Glue, Hive Metastore, Iceberg REST |
| Health dimensions          | file_size, delete_amplification, manifest_density, snapshot, partition_skew, format_version |
| Write-pattern classifier   | streaming / batch / mixed / unknown |
| Branch/tag-aware scanning  | `--branch <name>` flag              |
| Orphan-file discovery      | Bloom-filter + storage crawl, dry-run, grace period |
| PII scanner                | Parquet row-group sampling, regex + entropy, Atlas-compatible JSON |
| `sentry bench`             | baseline persist + before/after diff |
| CLI exit codes             | 0–5 per spec §3.1                   |
| Output formats             | `text`, `json`, `sarif`             |
| Policy-as-code             | `sentry.yaml`                       |
| GitHub Actions example     | SARIF upload + PR comment workflow  |
| Integration harness        | docker-compose for MinIO + REST + HMS |
| Tests                      | unit + end-to-end Avro/Parquet fixtures |

Phase 3 items (Prometheus exporter, Cloudera SDX/Atlas wiring, Migration
Readiness Audit, OAuth2/Kerberos for REST, disk-backed Bloom for 10M+
file tables) remain on the roadmap.

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
  cli/                 cobra commands (audit, orphans, pii, bench, version)
  iceberg/             metadata JSON + Avro manifest parsers
  storage/             local, S3, WebHDFS storage backends
  catalog/             LocalFS, Glue, Hive Metastore, REST adapters
  scan/                orchestration: catalog → metadata → manifests → stats
  health/              composite score + per-dimension diagnostics
  bloom/               double-hashed Bloom filter for orphan tracking
  writepattern/        streaming/batch classifier
  orphans/             differential storage-vs-metadata scan
  pii/                 Parquet row-group sampler + regex/entropy detector
  output/              text, json, SARIF renderers
  policy/              sentry.yaml parser and threshold application
  exitcode/            shared exit-code constants
examples/
  sentry.yaml                       sample policy
  github-actions/iceberg-health.yml SARIF + PR-comment workflow
scripts/gen_fixtures.py             pyiceberg integration-fixture generator
testdata/integration/               docker-compose harness (MinIO + REST + HMS)
```

## License

Apache 2.0 — see [LICENSE](./LICENSE).
