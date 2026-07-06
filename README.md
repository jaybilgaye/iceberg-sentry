<div align="center">

# ⌬ Iceberg Sentry

**Iceberg-native lakehouse reliability linter.**
Health score, delete-file amplification, orphan files, PII, partition skew, cost.
From metadata alone — no compute cluster required.

[![Go Reference](https://pkg.go.dev/badge/github.com/jaybilgaye/iceberg-sentry.svg)](https://pkg.go.dev/github.com/jaybilgaye/iceberg-sentry)
[![Go Report Card](https://goreportcard.com/badge/github.com/jaybilgaye/iceberg-sentry)](https://goreportcard.com/report/github.com/jaybilgaye/iceberg-sentry)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](./LICENSE)
[![CI](https://github.com/jaybilgaye/iceberg-sentry/actions/workflows/ci.yml/badge.svg)](https://github.com/jaybilgaye/iceberg-sentry/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/jaybilgaye/iceberg-sentry?sort=semver)](https://github.com/jaybilgaye/iceberg-sentry/releases)

[Website](https://icebergsentry.io) · [Docs](https://icebergsentry.io/docs/) · [Examples](https://icebergsentry.io/examples.html) · [Spec](./Iceberg_Sentry_Spec_v2.md)

</div>

---

## Install

```sh
curl -sSL https://get.icebergsentry.io/install.sh | sh
```

Or via Docker:

```sh
docker pull ghcr.io/jaybilgaye/iceberg-sentry:latest
```

Or from source:

```sh
go install github.com/jaybilgaye/iceberg-sentry/cmd/iceberg-sentry@latest
```

Full instructions: [docs → Install](https://icebergsentry.io/docs/install.html).

## What it does

```sh
iceberg-sentry audit --catalog rest --rest http://catalog:8181 \
    --table finance.transactions
```

```
  Table: finance.transactions  │  Score: 61/100  WARNING
  ─────────────────────────────────────────────────────────────────
  [CRITICAL]  Delete File Amplification  12/20   1,842 delete files
              → 23% of scan I/O is delete merging. Run REWRITE_DATA.
  [WARNING]   Manifest Density            8/15   3,401 manifest files
  [WARNING]   Small Files                12/20   68% files < 64MB
  [OK]        Snapshot Health            14/15   12 snapshots, oldest 8d
  [OK]        Partition Skew             14/15   max skew 11%
  [OK]        Schema Evolution           10/10   No dangerous patterns
  [INFO]      Format Version              3/5    Iceberg v1 → v2 available
  ─────────────────────────────────────────────────────────────────
  Estimated monthly waste: ~$340  │  Scan time: 4.2s
```

## Features

| Capability                        | Command / flag                                 |
|-----------------------------------|------------------------------------------------|
| Table health score (6 dimensions) | `audit`                                        |
| Delete-file amplification         | `audit` — first-class dimension                |
| Partition skew (CV)               | `audit` — first-class dimension                |
| Orphan file discovery             | `orphans` (dry-run, grace period)              |
| PII scanning (regex + entropy)    | `pii` — zero-disk-persistence                  |
| Baseline / compare deltas         | `bench start` / `bench compare`                |
| HDFS → CDP migration audit        | `migration`                                    |
| Snapshot cost timeline            | `cost`                                         |
| Prometheus exporter               | `export` (`/metrics` + `/healthz`) or `--push-gateway` |
| Write-pattern classifier          | Streaming / batch / mixed / unknown            |
| SARIF for GitHub Code Scanning    | `--format sarif`                               |
| Policy as code                    | `--policy sentry.yaml`                         |

## Catalogs

- **LocalFS** — HadoopCatalog-style directory layout, for tests + air-gapped deploys.
- **AWS Glue** — IAM role; cross-account via STS.
- **Hive Metastore** — Thrift, with optional Kerberos via keytab.
- **Iceberg REST** — Polaris, Databricks Unity, Tabular, Nessie. Bearer + OAuth2 client-credentials.

## Storage backends

`s3://`, `hdfs://` (WebHDFS), `file://`. ADLS Gen2 / GCS via S3-compatible endpoints today; native drivers on the roadmap.

## Wire it into CI

```yaml
- name: Iceberg health gate
  run: |
    iceberg-sentry audit \
      --catalog glue --namespace finance \
      --policy sentry.yaml \
      --format sarif > iceberg.sarif

- uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: iceberg.sarif
    category: iceberg-health
```

Exit codes are stable and CI-friendly:

| Code | Meaning                                    |
|:----:|--------------------------------------------|
|  0   | OK                                         |
|  1   | WARNING (with `--fail-on warn`)            |
|  2   | CRITICAL                                   |
|  3   | Untagged PII found                         |
|  4   | Tool / config error                        |
|  5   | Catalog / storage connection failure       |

## Deploy in Kubernetes

```sh
helm install sentry deploy/helm \
  --set cronjob.enabled=true \
  --set exporter.enabled=true \
  --set serviceMonitor.enabled=true
```

Full manifests, Helm chart, and Grafana dashboard JSON under [`deploy/`](./deploy).

## Development

```sh
make test           # unit + race
make lint           # gofmt + vet + golangci-lint
make build          # static binary
make fixtures       # pyiceberg-generated Iceberg tables
make docker-dev     # container from source
make bench          # perf benchmarks
```

Go 1.23+ required.

## Repo layout

```
cmd/iceberg-sentry/       CLI entry point (version stamped at build)
internal/
  cli/                    cobra commands (audit, orphans, pii, bench, migration, cost, export)
  iceberg/                metadata JSON + Avro manifest parsers
  storage/                local, S3, WebHDFS
  catalog/                LocalFS, Glue, Hive (Thrift+Kerberos), REST (OAuth2)
  scan/                   orchestration engine
  health/                 composite scoring, per-dimension diagnostics
  bloom/                  double-hashed Bloom filter
  writepattern/           streaming/batch classifier
  orphans/                differential storage↔metadata scan
  pii/                    Parquet row-group sampler + regex/entropy
  migration/              HDFS → CDP audit
  cost/                   pluggable cost provider + snapshot timeline
  metrics/                Prometheus exposition + push gateway
  output/                 text · json · sarif · prometheus renderers
  policy/                 sentry.yaml
deploy/
  k8s/                    raw CronJob + Deployment + ServiceMonitor
  helm/                   Helm chart
  grafana-dashboard.json
site/                     marketing site + 15-page documentation
scripts/
  gen_fixtures.py         pyiceberg integration fixtures
  install.sh              curl-pipe-safe installer
```

## Roadmap

Phase 1 (foundation), Phase 2 (advanced auditing), and Phase 3 (Cloudera + operationalization) are shipped. Phase 4 — hosted SaaS MVP, Nessie branch-history, Slack/PagerDuty alerts, dbt + OpenMetadata/DataHub plugins — is the next milestone.

See [`Iceberg_Sentry_Spec_v2.md`](./Iceberg_Sentry_Spec_v2.md) for the full product spec and [`CHANGELOG.md`](./CHANGELOG.md) for the release history.

## Contributing

Contributions welcome — see [`CONTRIBUTING.md`](./CONTRIBUTING.md). Everyone in the community is expected to follow the [Code of Conduct](./CODE_OF_CONDUCT.md). Security issues: please read [`SECURITY.md`](./SECURITY.md) and use GitHub Security Advisories, not public issues.

## License

Apache 2.0. See [`LICENSE`](./LICENSE) and [`NOTICE`](./NOTICE).
