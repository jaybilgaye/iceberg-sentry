# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] — 2026-05-14 — Phase 3

### Added
- **Prometheus exporter** — `audit --format prometheus` for one-shot push to a Pushgateway,
  and a long-lived `export` server on `/metrics` + `/healthz`.
- **Migration Readiness Audit** — `migration` command. Detects absolute `hdfs://` paths,
  HDFS-specific table properties, and v1 tables that should be upgraded pre-migration.
  Per-table `LOW` / `MEDIUM` / `HIGH` risk score with concrete remediation.
- **Snapshot cost timeline** — `cost` command with pluggable `cost.Provider`
  (S3 Standard, IA, Glacier-Instant, Glacier-Deep list-price defaults).
- **Atlas health-payload export** — `audit --atlas-output` mirrors the PII Atlas envelope
  for downstream Atlas / Unity Catalog bulk import.
- **OAuth2 client-credentials** for REST catalog (Polaris / Databricks Unity).
  Token cached with 30 s refresh window.
- **Kerberos transport** for Hive Metastore via `gokrb5` — keytab-driven, `krb5.conf`-aware.
- Kubernetes `CronJob` + `Deployment` + `ServiceMonitor` YAMLs under `deploy/k8s/`.
- Helm chart under `deploy/helm/` with independently toggleable CronJob and exporter modes.
- Grafana dashboard JSON at `deploy/grafana-dashboard.json`.
- Performance-regression CI workflow (`benchstat`-driven, > 20 % gate).
- Marketing site + 15-page docs under `site/`.

## [0.2.0] — Phase 2

### Added
- Orphan-file discovery with Bloom-filter tracking, dry-run default, grace-period safety.
- PII scanner with Parquet row-group sampling and regex + Shannon-entropy detection
  (email, Luhn-validated cards, SSN, phone, AU TFN, IN Aadhaar, API keys).
  Zero-disk-persistence guarantee.
- `bench` command — capture baseline, run maintenance, compare deltas.
- Iceberg REST catalog adapter (Polaris / Tabular / Unity / Nessie).
- Partition-skew health dimension (coefficient of variation, hot / sparse counts).
- Write-pattern classifier (streaming / batch / mixed) feeding threshold adjustment.
- Branch / tag-aware scanning via `audit --branch <name>`.
- SARIF 2.1.0 output format for GitHub Code Scanning.
- GitHub Actions example workflow with SARIF upload + PR comment.
- Integration harness — docker-compose for MinIO + Iceberg REST + Hive Metastore.

## [0.1.0] — Phase 1

### Added
- Iceberg metadata JSON parser (v1, v2, v3 forward-compatible).
- Streaming Avro manifest-list and manifest-file readers (`hamba/avro/v2`).
- Storage Abstraction Layer with LocalFS, S3 (`aws-sdk-go-v2`), and WebHDFS backends.
- Catalog adapters: LocalFS, AWS Glue, Hive Metastore (minimal Thrift client).
- Composite health score across five dimensions: `file_size`, `delete_amplification`,
  `manifest_density`, `snapshot`, `format_version`.
- Text and JSON output renderers.
- `sentry.yaml` policy parser with namespace globs and threshold overrides.
- Unit + end-to-end test suites; pyiceberg fixture-generation script.

[Unreleased]: https://github.com/jaybilgaye/iceberg-sentry/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/jaybilgaye/iceberg-sentry/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/jaybilgaye/iceberg-sentry/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jaybilgaye/iceberg-sentry/releases/tag/v0.1.0
