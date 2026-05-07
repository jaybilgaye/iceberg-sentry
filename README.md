# Iceberg Sentry

Iceberg-native lakehouse reliability linter — see
[`Iceberg_Sentry_Spec_v2.md`](./Iceberg_Sentry_Spec_v2.md) for the full
product specification.

This repository contains the **Phase 1 foundation**: an end-to-end working
CLI that scores Apache Iceberg table health from metadata alone, with no
compute engine required.

## What's in Phase 1

| Capability                 | Status                              |
|----------------------------|-------------------------------------|
| Iceberg metadata JSON      | v1, v2, v3 (forward-compat parsing) |
| Streaming Avro manifests   | manifest-list and manifest-file      |
| Storage backends           | local FS, S3, WebHDFS                |
| Catalogs                   | LocalFS, AWS Glue, Hive Metastore    |
| Health dimensions          | file_size, delete_amplification, manifest_density, snapshot, format_version |
| CLI exit codes             | 0–5 per spec §3.1                   |
| Output formats             | `text`, `json`                       |
| Policy-as-code             | `sentry.yaml`                       |
| Tests                      | unit + end-to-end against synthetic Avro fixtures |

Phase 2+ items (orphan-file discovery, PII scanner, partition skew, REST
catalog, SARIF/Prometheus output, `sentry bench`) are not yet implemented;
hooks are in place where they fit.

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
  cli/                 cobra commands and exit-code mapping
  iceberg/             metadata JSON + Avro manifest parsers
  storage/             local, S3, WebHDFS storage backends
  catalog/             LocalFS, Glue, Hive Metastore adapters
  scan/                orchestration: catalog → metadata → manifests → stats
  health/              composite score and per-dimension diagnostics
  output/              text and json renderers
  policy/              sentry.yaml parser and threshold application
  exitcode/            shared exit-code constants
examples/sentry.yaml   sample policy
scripts/gen_fixtures.py pyiceberg integration-fixture generator
```

## License

Apache 2.0 — see [LICENSE](./LICENSE).
