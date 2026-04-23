# Iceberg Sentry: Technical & Feature Specification v2.0
**Positioning**: Iceberg-Native Lakehouse Reliability Platform · Cloudera / CDP Aware  
**Author**: Jayprakash Bilgaye  
**Date**: April 2026  
**Status**: Working Draft

---

## Page 1: Executive Summary & Strategic Vision

### 1.1 The Problem: "Day 2" Lakehouse Operations at Scale

Apache Iceberg solved the original problem — ACID transactions and schema evolution on object storage. It created a new one: nobody built the operational tooling to keep Iceberg tables healthy over time.

Enterprises running production lakehouses — whether on Cloudera CDP, AWS Glue, Databricks, or bare Iceberg REST Catalog — consistently face the same set of "Day 2" failure modes:

| Problem | Root Cause | Real-World Impact |
|---|---|---|
| **Metadata Bloat** | Manifest file accumulation from streaming writes and frequent commits | 10x increase in S3 LIST API costs; metadata read latency kills query warm-up |
| **Delete File Amplification** | Iceberg v2 position/equality delete files accumulate without compaction | Query engines must merge deletes at scan time — 5–20x read amplification |
| **Orphaned Data** | Parquet files persist in storage after snapshots expire | "Dark storage" — paying for data no query can ever reach |
| **Governance Gaps** | PII lives in Iceberg columns that are untagged in catalogs | Compliance risk; Atlas/Unity policies protect nothing they can't see |
| **Partition Skew** | Poor initial partitioning or data growth patterns | Hot partitions cause query bottlenecks and executor OOM |
| **Schema Drift** | Schema evolution applied without best-practice checks | Silent data corruption or expensive full-table rewrites |
| **Migration Blindness** | On-prem to cloud migrations proceed without Iceberg metadata validation | Broken table paths, incompatible properties, surprise cost spikes post-migration |

No lightweight, engine-agnostic tool addresses all of these in a single audit pass. The existing options are either too expensive (Monte Carlo, Acceldata), too broad (OpenMetadata, DataHub), or require a running Spark cluster just to run basic maintenance.

### 1.2 The Solution: Iceberg Sentry

**Iceberg Sentry** is a high-performance, stateless, engine-agnostic **linter and diagnostic engine** for Apache Iceberg tables. It audits the physical and metadata health, performance, and cost efficiency of Iceberg tables — without requiring Spark, Flink, or any compute cluster to be running.

**Core design principles:**
- **Engine-agnostic**: Reads Iceberg metadata directly. No Spark context required.
- **Shift-left**: Integrates natively into CI/CD pipelines via structured SARIF output and well-defined exit codes.
- **Safe by default**: All destructive recommendations require explicit `--confirm` flags. Dry-run is always available.
- **Lightweight**: < 512MB RAM for tables with millions of files, via streaming algorithms and Bloom Filter-based tracking.
- **Multi-catalog**: Works with Hive Metastore, AWS Glue, Iceberg REST Catalog, Polaris (Snowflake Open Catalog), and Unity Catalog.

### 1.3 What Iceberg Sentry Is Not

This positioning matters. Iceberg Sentry is **not**:
- A data catalog (not competing with Polaris, Nessie, Unity Catalog)
- A data quality platform (not competing with Great Expectations, Soda)
- An observability platform (not competing with Monte Carlo, Acceldata)
- A query engine or compaction tool (it recommends; it does not execute compaction itself)

It is the **diagnostic layer that feeds all of the above** with actionable, structured health intelligence.

### 1.4 Competitive Positioning

| Tool | Category | Weakness vs. Iceberg Sentry |
|---|---|---|
| Monte Carlo | Data Observability (SaaS) | Expensive, requires data pipeline integration, no Iceberg-native metadata analysis |
| OpenMetadata | Metadata Management | Broad-purpose catalog; no physical health scoring or delete file analysis |
| DataHub | Metadata Management | LinkedIn-scale complexity; heavy deployment; no storage cost analysis |
| Bigeye | Data Quality | Focuses on row-level data quality, not table physical health |
| Acceldata | Data Reliability | Enterprise-only SaaS; opaque scoring; no CLI or CI/CD integration |
| Spark `RemoveOrphanFiles` | Built-in Maintenance | Requires a running cluster; no health scoring, no PII detection, no cost modeling |

**Iceberg Sentry's moat**: The only tool that is simultaneously engine-agnostic, shift-left CI/CD-native, and provides delete file amplification analysis alongside metadata health, PII discovery, and cost modeling — in a single < 20MB binary.

### 1.5 Strategic Alignment: Cloudera CDP

For Cloudera customers specifically, Iceberg Sentry acts as a "Pre-flight Scanner" that enriches the Shared Data Experience (SDX):
- Feeds metadata health, PII discovery, and performance insights into Apache Atlas.
- Respects Apache Ranger policies via Kerberos/OAuth2 identity passthrough.
- Provides a **Migration Readiness Audit** for on-prem HDFS → CDP Public Cloud migrations.
- Exposes Prometheus metrics for Cloudera Manager and Grafana dashboards.

---

## Page 2: Core Feature Set

### 2.1 Table Health Score (0–100)

The **Table Health Score** is the primary output of every Iceberg Sentry scan. It is a weighted composite score derived from seven health dimensions. This score is designed to be actionable — not just a number, but a ranked list of issues with remediation commands.

**Score Dimensions and Weights:**

| Dimension | Weight | What It Measures |
|---|---|---|
| File Size Distribution | 20% | Small file syndrome (< 128MB) and oversized files |
| Delete File Amplification | 20% | Ratio of equality/position delete files to data files (v2 tables) |
| Manifest Density | 15% | Manifest file count relative to data file count |
| Snapshot Age & Count | 15% | Unreferenced historical snapshots; total snapshot chain depth |
| Partition Skew | 15% | Coefficient of variation across partition data volumes |
| Schema Evolution Compliance | 10% | Dangerous schema change patterns detected |
| Table Format Version | 5% | v1 tables that could benefit from v2 upgrade |

**Sample CLI Output:**
```
$ iceberg-sentry audit --table finance.transactions --catalog hive

  Table: finance.transactions  │  Score: 61/100  ⚠ WARNING
  ─────────────────────────────────────────────────────────────────
  [CRITICAL]  Delete File Amplification  12/20   1,842 delete files
              → 23% of scan I/O is delete merging. Run REWRITE_DATA.
              → ALTER TABLE finance.transactions EXECUTE rewrite_data_files(...)
  
  [WARNING]   Manifest Density            8/15   3,401 manifest files
              → Exceeds 2,000 manifest threshold. Run rewrite_manifests.
  
  [WARNING]   Small Files                12/20   68% files < 64MB
              → 4,201 files below optimal size. Compaction recommended.

  [OK]        Snapshot Health            14/15   12 snapshots, oldest 8d
  [OK]        Partition Skew             14/15   max skew 11%
  [OK]        Schema Evolution           10/10   No dangerous patterns
  [INFO]      Format Version              3/5    Iceberg v1 — v2 upgrade available
  ─────────────────────────────────────────────────────────────────
  Estimated monthly waste: ~$340  │  Scan time: 4.2s
```

### 2.2 Delete File Amplification Analysis *(First-Class Feature)*

This is the most impactful and most overlooked operational metric for Iceberg v2 tables.

Every time a row is updated or deleted in an Iceberg v2 table, Iceberg writes a **delete file** (position delete or equality delete) rather than rewriting the affected data file. Over time, these accumulate:

- Query engines must perform a merge-on-read at scan time for every affected data file.
- A table with 10,000 data files and 5,000 delete files forces the engine to join every scan with thousands of additional file reads.
- This is silent — no error is thrown, queries just get progressively slower.

**Iceberg Sentry measures:**
- Total delete file count (position vs. equality, split separately)
- Delete-to-data file ratio per partition
- Estimated read amplification factor (multiplier on base scan cost)
- Age of oldest uncompacted delete file

**Severity thresholds (configurable):**

| Ratio | Severity | Recommended Action |
|---|---|---|
| < 5% | OK | Monitor |
| 5–15% | Warning | Schedule compaction |
| 15–30% | High | Compaction required soon |
| > 30% | Critical | Immediate compaction; queries severely impacted |

**Output includes a ready-to-run remediation command** for Spark SQL, targeting the specific partitions with the highest delete ratios.

### 2.3 Table Format Version Detection

Iceberg v1, v2, and v3 have substantially different capabilities and performance characteristics. Production tables often have inconsistent format versions across a namespace, especially after catalog migrations.

**Iceberg Sentry checks:**
- Table format version (from `format-version` in metadata JSON)
- Whether v1 tables are using features that require v2 (row-level deletes via workarounds)
- Whether v2 tables are correctly configured for their declared delete mode (merge-on-read vs. copy-on-write)
- v3 features availability check (row lineage, variant type)

**Upgrade recommendation logic:**
```
v1 table + CDC/streaming writes detected → Recommend upgrade to v2
v2 table + copy-on-write declared + high update frequency → Recommend switching to merge-on-read
v2 table + no delete files in 90d → Flag potential over-provisioning of delete mode
```

### 2.4 Partition Skew Detection

Analyzes data volume and record count distribution across all partitions.

- **Hot Partitions**: Partitions > 2x the median volume — likely query bottlenecks.
- **Sparse Partitions**: Partitions < 10% of median volume — inefficient file layout.
- **Skew Coefficient**: Reports the coefficient of variation (CV) across all partitions. CV > 0.5 triggers a warning.
- **Temporal Skew Analysis**: For date/timestamp partitions, plots data volume over time to distinguish "expected growth" from "write amplification anomalies."
- **Actionable Output**: Specific partition re-partitioning recommendations with estimated file counts post-optimization.

### 2.5 Orphan File Discovery (with Safety Controls)

Performs a differential scan between Iceberg metadata and physical storage to identify files that exist on disk but are not referenced by any valid snapshot.

**Workflow:**
1. **Metadata Walk**: Build a Bloom Filter of all files referenced in the current and all valid snapshots.
2. **Storage Crawl**: Stream file listings from S3/ADLS/GCS/HDFS.
3. **Differential Report**: Files in storage but not in Bloom Filter = orphan candidates.

**Critical Safety Controls** (these are non-negotiable):

| Control | Default | Override |
|---|---|---|
| **Dry-run mode** | Always on unless `--confirm` passed | `--confirm` |
| **Grace period** | Files < 24h old are never flagged | `--grace-period 6h` |
| **Snapshot isolation** | Scan operates on a locked snapshot timestamp | Not configurable |
| **Output format** | JSON manifest of orphan candidates | `--format csv` |
| **Size summary** | Total reclaimable bytes shown before any action | Always shown |

**Dry-run output:**
```
$ iceberg-sentry orphans --table finance.transactions --dry-run

  [DRY RUN] Orphan File Report: finance.transactions
  ───────────────────────────────────────────────────
  Scan snapshot:   2026-04-24T09:14:22Z  (locked)
  Grace period:    24h  (files after 2026-04-23T09:14:22Z excluded)
  
  Found 847 orphan files  │  Reclaimable: 12.4 GB  │  Est. savings: ~$3.60/mo
  
  Preview (first 5):
  s3://datalake/finance/transactions/data/00123.parquet  (2.1 MB, 18d old)
  s3://datalake/finance/transactions/data/00124.parquet  (1.8 MB, 18d old)
  ...
  
  To delete: iceberg-sentry orphans --table finance.transactions --confirm
  Full manifest: orphans_finance_transactions_20260424.json
```

### 2.6 Write Pattern Analysis

Different write patterns produce fundamentally different file layouts. A health score must be aware of write context or it will generate false positives.

**Iceberg Sentry infers write pattern from metadata:**
- **Streaming pattern**: Many small commits, high manifest count, files uniformly small → Adjust health scoring thresholds; recommend streaming-optimized compaction schedule.
- **Batch pattern**: Larger files, periodic snapshot growth → Standard thresholds apply.
- **Mixed pattern**: Evidence of both (e.g., Flink streaming + nightly Spark batch) → Flag for review; compaction strategy depends on which pattern dominates by volume.

**Why this matters:** A Flink streaming table that is 3 hours old should not be flagged as having "Small File Syndrome." Without write pattern awareness, Iceberg Sentry would report it as unhealthy when it is actually behaving correctly.

The write pattern classification is surfaced in every scan output and directly modifies the health score thresholds used for that table.

### 2.7 Metadata Bloat Alerts

- **Manifest File Count**: Configurable alert threshold. Default: warn at 2,000, critical at 5,000.
- **Snapshot Count & Age**: Alert when snapshot chain depth or oldest snapshot age exceed policy.
- **Manifest List Size**: Tracks the size of the manifest list file itself — a proxy for metadata read overhead.
- **Automated Remediation Commands**: Every alert includes the exact SQL/API call to resolve it.

### 2.8 Shadow PII Detection

Proactively finds PII in Iceberg columns that are not yet tagged in the catalog.

- **Sampling Strategy**: Reads a configurable percentage (default 5%) of Parquet row groups from the latest snapshot only. Never reads entire files.
- **Detection Methods**: High-speed regex (SSNs, credit card numbers, email patterns, phone numbers, Australian TFNs, Indian Aadhaar) plus entropy-based detection for API keys and tokens.
- **Zero-persistence**: All sampling is in-memory, stream-processed. No sensitive data is ever written to disk or logged.
- **Catalog Sync Output**: Generates a structured JSON payload for bulk import into Apache Atlas, AWS Glue Data Catalog, or Unity Catalog. Manual review step is required before import — no silent auto-tagging.

**Atlas payload sample:**
```json
{
  "review_required": true,
  "generated_at": "2026-04-24T09:14:22Z",
  "findings": [
    {
      "table": "finance.transactions",
      "column": "customer_email",
      "pii_type": "EMAIL",
      "confidence": 0.97,
      "sample_count": 412,
      "recommended_tag": "PII_EMAIL",
      "atlas_entity_guid": "abc-123"
    }
  ]
}
```

### 2.9 Query Performance Diagnostics (Predictive)

Analyzes table structure and metadata to identify performance inhibitors *before* queries run — no query log required.

- **File Layout Analysis**: Predicts scan efficiency based on file size distribution, data clustering, and sort order within files.
- **Partition Pruning Effectiveness**: Evaluates whether the current partitioning scheme allows effective pruning for common query patterns (inferred from partition column cardinality and data distribution).
- **Bloom Filter & Statistics Coverage**: Checks whether column-level statistics (min/max) are present in Parquet footers. Missing statistics force full file scans.
- **Delete Merge Cost Estimate**: Quantifies the estimated I/O overhead from accumulated delete files (see Section 2.2).

### 2.10 Cost Optimization Insights

- **Storage Cost Savings**: Quantifies reclaimable storage from orphan files. Displays in bytes and estimated monthly cost (user-configurable $/GB/month).
- **Compute Cost Reduction**: Estimates query performance improvement and associated compute savings from compaction and re-partitioning.
- **Snapshot Cost Timeline**: Plots cumulative storage cost growth over the snapshot history, identifying when the table's storage footprint started growing abnormally.
- **Tiered Storage Recommendations**: Flags snapshots older than configurable thresholds as candidates for transition to Glacier/cold tiers.

### 2.11 `sentry bench` — Before/After Validation

A dedicated command that runs a health audit, captures a baseline, and re-runs after a maintenance operation to measure actual improvement.

```bash
# Before compaction
iceberg-sentry bench start --table finance.transactions --tag pre-compaction

# Run your compaction (Spark SQL, CDE job, etc.)
spark-sql -e "CALL catalog.system.rewrite_data_files('finance.transactions')"

# After compaction
iceberg-sentry bench compare --table finance.transactions --tag pre-compaction

Output:
  Benchmark: finance.transactions  │  pre-compaction → post-compaction
  ────────────────────────────────────────────────────────────────────
  Health Score:         61  →  89   (+28 pts)
  Delete Files:      1,842  →  0    (-100%)
  Manifest Files:    3,401  →  124  (-96%)
  File Count:       18,203  →  2,847 (-84%)
  Avg File Size:     38 MB  →  241 MB
  Est. Scan Cost:   $0.041  →  $0.008  (-80%)
```

This is the feature most likely to get referenced in a job interview or blog post — it closes the loop between diagnosis and remediation.

### 2.12 Policy-as-Code (`sentry.yaml`)

Define table requirements as code, version-controlled alongside your data pipelines.

```yaml
version: "1.0"
default_catalog: hive-metastore

policies:
  - name: "finance-compliance"
    target_namespace: "finance.*"
    max_snapshot_age: "30d"
    min_file_size_mb: 128
    max_manifest_files: 500
    max_partition_skew_percent: 20
    pii_scan: true
    fail_on_orphans: false          # Report but don't fail CI
    fail_on_pii_untagged: true      # Fail CI if untagged PII found
    delete_file_ratio_warn: 0.10
    delete_file_ratio_fail: 0.25
    write_pattern: auto             # auto-detect; or streaming | batch
    min_health_score: 75            # CI fails if score drops below 75

  - name: "raw-zone-relaxed"
    target_namespace: "raw.*"
    max_snapshot_age: "7d"
    min_file_size_mb: 32            # Relaxed for streaming ingestion tables
    write_pattern: streaming
    min_health_score: 50
    pii_scan: true
    fail_on_pii_untagged: true
```

---

## Page 3: CI/CD Integration ("Shift-Left")

This is Iceberg Sentry's most distinctive capability relative to any existing tool. It enables data quality gates in the same way `eslint` and `bandit` enable code quality gates.

### 3.1 Exit Code Specification

| Exit Code | Meaning |
|---|---|
| `0` | All checks passed |
| `1` | Warning threshold exceeded (configurable: treat as pass or fail) |
| `2` | Critical threshold exceeded — hard failure |
| `3` | PII found in untagged columns |
| `4` | Tool configuration error |
| `5` | Catalog/storage connection failure |

### 3.2 Output Formats

| Format | Flag | Use Case |
|---|---|---|
| Human-readable | `--format text` (default) | Terminal / developer |
| JSON | `--format json` | Programmatic consumption |
| SARIF 2.1.0 | `--format sarif` | GitHub Code Scanning, Azure DevOps, GitLab SAST |
| Prometheus | `--format prometheus` | Metrics scraping |
| Markdown | `--format markdown` | PR comments via CI bot |

**SARIF output** allows Iceberg Sentry findings to appear natively in GitHub's Security tab alongside code scanning results — a powerful shift-left signal.

### 3.3 GitHub Actions Integration

```yaml
# .github/workflows/iceberg-health.yml
name: Iceberg Table Health Gate

on:
  push:
    paths:
      - 'pipelines/finance/**'
  schedule:
    - cron: '0 6 * * *'          # Daily 6 AM scan

jobs:
  iceberg-audit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install Iceberg Sentry
        run: |
          curl -sSL https://get.icebergsentry.io/install.sh | bash
          echo "$HOME/.local/bin" >> $GITHUB_PATH

      - name: Run Iceberg Health Audit
        env:
          AWS_ROLE_ARN: ${{ secrets.ICEBERG_AUDIT_ROLE }}
          GLUE_CATALOG_ID: ${{ secrets.AWS_ACCOUNT_ID }}
        run: |
          iceberg-sentry audit \
            --policy sentry.yaml \
            --namespace finance \
            --format sarif \
            --output iceberg-results.sarif

      - name: Upload SARIF to GitHub Security
        uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: iceberg-results.sarif
          category: iceberg-health

      - name: Post PR Comment
        if: github.event_name == 'pull_request'
        run: |
          iceberg-sentry audit \
            --policy sentry.yaml \
            --namespace finance \
            --format markdown | gh pr comment ${{ github.event.number }} --body-file -
```

### 3.4 Pre-commit Hook Example

```bash
# .pre-commit-config.yaml (for dbt / SQL-based lakehouse projects)
repos:
  - repo: local
    hooks:
      - id: iceberg-sentry
        name: Iceberg Table Health Check
        entry: iceberg-sentry audit --policy sentry.yaml --namespace staging --fail-on warn
        language: system
        pass_filenames: false
```

---

## Page 4: Technical Architecture

### 4.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        iceberg-sentry CLI                           │
│                                                                     │
│  ┌─────────────┐    ┌──────────────────┐    ┌───────────────────┐  │
│  │   Policy    │    │  Catalog         │    │   Output          │  │
│  │   Engine    │    │  Resolver        │    │   Formatter       │  │
│  │ (sentry.yaml│    │  (HMS/Glue/REST/ │    │  (text/JSON/      │  │
│  │  validator) │    │  Polaris/Unity)  │    │   SARIF/prom)     │  │
│  └──────┬──────┘    └────────┬─────────┘    └────────▲──────────┘  │
│         │                   │                        │             │
│         ▼                   ▼                        │             │
│  ┌──────────────────────────────────────────────────┐│             │
│  │              Metadata Parser                     ││             │
│  │  • JSON metadata walker (vN.metadata.json)       ││             │
│  │  • Avro manifest reader (streaming, zero-copy)   ││             │
│  │  • Bloom Filter: active file set (disk-backed    ││             │
│  │    KV for tables > 10M files)                   ││             │
│  │  • Write pattern classifier                      ││             │
│  └────────────────────────┬─────────────────────────┘│             │
│                           │                          │             │
│         ┌─────────────────┼─────────────────┐        │             │
│         ▼                 ▼                 ▼        │             │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────┐│             │
│  │  Concurrency│  │  Diagnostic  │  │  Storage     ││             │
│  │  Engine     │  │  Module      │  │  Abstraction ││             │
│  │  (worker    │  │  (health     │  │  Layer       ││             │
│  │  pool,      │  │  scoring,    │  │  S3/ADLS/    ││             │
│  │  fan-out    │  │  skew, cost, │  │  GCS/HDFS)   ││             │
│  │  manifests) │  │  delete amp) │  │              ││             │
│  └─────────────┘  └──────────────┘  └──────────────┘│             │
│                                                      │             │
│                   Results aggregated ────────────────┘             │
└─────────────────────────────────────────────────────────────────────┘
```

### 4.2 The Deep Scan Workflow

1. **Policy Load**: Parse and validate `sentry.yaml`. Resolve target namespaces and apply per-table policy overrides.
2. **Catalog Resolution**: Connect to the specified catalog (HMS, Glue, Iceberg REST, Polaris, Unity). Fetch table metadata location.
3. **Metadata Snapshot Lock**: Download `vN.metadata.json`. Lock the scan to the latest complete snapshot to guarantee point-in-time consistency. **In-flight writes do not affect the audit.**
4. **Write Pattern Classification**: Analyse snapshot history (commit frequency, files-per-commit, time gaps) to classify the table as streaming, batch, or mixed.
5. **Parallel Manifest Walk**:
   - Worker A: Parses Manifest List → dispatches manifest file URLs to worker pool.
   - Workers B–Z: Concurrently parse individual Manifest Files (Avro, streaming) → build Bloom Filter of active data file paths + collect per-file statistics (size, record count, partition values, delete file type).
   - Bloom Filter backed by disk-based KV store (e.g., bbolt) when active file count exceeds 500,000 to prevent OOM.
6. **Storage Crawl** (orphan mode only): Streams object listing from storage provider. Uses HTTP Range Requests to minimise API calls. Checks each listed path against Bloom Filter.
7. **Diagnostic Analysis**: Feeds collected statistics into the Diagnostic Module to compute health scores, delete amplification ratios, skew coefficients, and cost models.
8. **PII Sampling** (if enabled): Worker pool selectively reads Parquet footers then samples a configurable % of row groups. All processing in-memory; zero writes to disk.
9. **Output Generation**: Formats results per requested output format. Applies policy thresholds to determine exit code.

### 4.3 Performance Targets

| Metric | Target |
|---|---|
| Metadata scan (1,000 manifests) | < 10 seconds |
| Metadata scan (100,000 manifests) | < 90 seconds |
| Memory footprint (standard) | < 256MB RAM |
| Memory footprint (10M+ file tables) | < 512MB RAM (disk-backed KV) |
| Binary size | < 20MB (single static binary) |
| API call reduction | 80% reduction vs. naive listing via HTTP Range Requests |

### 4.4 Testing Strategy *(Senior Signal)*

This section is a first-class deliverable, not an afterthought.

**Unit Tests:**
- Mock filesystem (in-memory `afero`-based FS) for all storage operations.
- Fuzz testing on Avro manifest parser to catch malformed input.
- Property-based tests for Bloom Filter correctness at 1M, 10M, 100M entries.
- Delete amplification calculator tested against known Iceberg spec examples.

**Integration Tests:**
- Local MinIO instance (S3-compatible) provisioned via `docker-compose` for CI.
- Test fixture tables generated with `pyiceberg`: v1 table, v2 table with deletes, streaming table (100k manifests), skewed table (90/10 partition split).
- Hive Metastore Standalone container for catalog integration tests.
- Assertion framework: `sentry bench` baseline + mutation + re-scan, asserting score delta.

**Benchmark Test Harness:**
- `iceberg-sentry bench` command doubles as a performance regression test.
- Generates deterministic test tables of configurable sizes and verifies scan time and memory usage stay within targets.
- Run in CI on PR to main; alert if scan time regresses > 20%.

**Security Tests:**
- Verify PII scanner produces zero disk writes under `strace`.
- Verify no sensitive column values appear in any log output.
- Fuzz credential handling paths.

---

## Page 5: Catalog & Ecosystem Integrations

### 5.1 Supported Catalogs

| Catalog | Phase | Notes |
|---|---|---|
| Apache Hive Metastore | Phase 1 | Thrift protocol, Kerberos/SASL |
| AWS Glue Data Catalog | Phase 1 | IAM role-based; cross-account via STS |
| Iceberg REST Catalog | Phase 1 | Standard REST spec; token or OAuth2 |
| Snowflake Open Catalog (Polaris) | Phase 2 | REST Catalog spec compliant |
| Databricks Unity Catalog | Phase 2 | REST Catalog spec + Unity extensions |
| Project Nessie | Phase 3 | Branch/tag-aware scanning (see 5.3) |

### 5.2 Storage Backends

S3, ADLS Gen2, GCS, HDFS — via a unified **Storage Abstraction Layer (SAL)**. The SAL exposes a single streaming iterator interface regardless of backend, handling:
- Pagination of object listings
- HTTP Range Requests for Parquet footer and Avro header reads
- Retry logic with exponential backoff
- Credential refresh for long-running scans

### 5.3 Iceberg Branch & Tag Awareness

Iceberg REST Catalog supports branches and tags (introduced in the Iceberg spec). Tables using branching workflows cannot be correctly audited by scanning only the "main" branch.

**Iceberg Sentry handles this:**
- `--branch <name>` flag to scan a specific branch's snapshot chain.
- `--scan-all-branches` mode to report health per branch (useful for detecting stale branches with orphan data).
- Default behaviour (no flag): scans the `main` branch only, with a warning if other branches exist.

### 5.4 Cloudera SDX Integration

**Apache Ranger**: Iceberg Sentry authenticates using the operator's Kerberos ticket or OAuth2 token. It never bypasses Ranger policies — if the scanning identity cannot access a table, the scan fails with an explicit permission error rather than silently skipping.

**Apache Atlas**: PII detection and health scores are exported as a structured JSON payload suitable for bulk import via the Atlas REST API (`POST /api/atlas/v2/entity/bulk`). The workflow is:
1. Iceberg Sentry outputs `atlas-import-YYYYMMDD.json`.
2. Data steward reviews findings in the JSON (or in a simple HTML report).
3. Steward imports to Atlas via CLI: `atlas-import atlas-import-YYYYMMDD.json`.
4. Atlas entities are updated with `Health`, `PII_Risk`, and `Performance` custom attributes.

This two-step approach (generate then import with human review) is more realistic than fully automated tagging and avoids permission escalation concerns.

**Cloudera Manager / Ops**: Prometheus Exporter mode exposes all health metrics for ingestion into Cloudera Manager's existing Prometheus-compatible monitoring stack, with pre-built Grafana dashboard JSON included in the repository.

### 5.5 Migration Readiness Audit (On-Prem → CDP Public Cloud)

A dedicated report template for Cloudera customers planning HDFS → S3/ADLS migrations:
- Validates that all Iceberg metadata paths are relative (not absolute HDFS paths).
- Detects table properties that are HDFS-specific and will break on S3 (e.g., `write.metadata.path`).
- Flags v1 tables that should be upgraded to v2 before migration (cheaper to do on-prem).
- Generates a per-table migration risk score (Low / Medium / High).
- Estimates post-migration storage costs based on current table size and orphan percentage.

---

## Page 6: Deployment Models

### 6.1 CLI (Primary)

Single static binary, < 20MB. Distributed via:
- `curl | bash` installer
- `brew install iceberg-sentry` (macOS/Linux)
- GitHub Releases (versioned binaries for linux/amd64, linux/arm64, darwin/arm64, windows/amd64)
- Docker image: `ghcr.io/icebergsentry/sentry:latest`

### 6.2 Kubernetes Operator / CDE CronJob

Runs as a periodic audit job in Cloudera Data Engineering or any Kubernetes environment. Outputs health metrics to Prometheus and sends alerts to Slack/PagerDuty via configurable webhook.

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: iceberg-sentry-daily-audit
spec:
  schedule: "0 2 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: sentry
            image: ghcr.io/icebergsentry/sentry:latest
            args:
              - audit
              - --policy=/config/sentry.yaml
              - --namespace=finance
              - --format=prometheus
              - --push-gateway=http://prometheus-pushgateway:9091
```

### 6.3 Prometheus Exporter (Continuous Monitoring)

Runs as a long-lived sidecar or standalone service, exposing all health metrics on `:9400/metrics`. Pre-built Grafana dashboard JSON included.

Key metrics exposed:
```
iceberg_table_health_score{table, namespace, catalog}
iceberg_delete_file_ratio{table, namespace}
iceberg_manifest_count{table, namespace}
iceberg_orphan_file_bytes{table, namespace}
iceberg_pii_untagged_columns{table, namespace}
iceberg_partition_skew_coefficient{table, namespace}
```

### 6.4 SaaS Concept (Future Tier)

A hosted version — "Iceberg Sentry Cloud" — where users connect their cloud storage credentials via IAM role delegation (no credentials stored). The platform:
- Runs scheduled scans on user-defined cadences.
- Provides a web dashboard with trend graphs, cross-table health rankings, and cost savings summaries.
- Sends Slack/email alerts on health score drops.

This tier is consistent with the KafkaGuard.com and ClusterSight.io models and would be built on the same operational patterns.

---

## Page 7: Security & Safety Design

### 7.1 Credential Management

- Credentials are **never stored** by Iceberg Sentry. They are resolved at runtime from environment variables, `~/.aws/credentials`, Kubernetes service accounts, or Vault.
- For CDP environments, Kerberos tickets are used via the standard `kinit` flow.
- The scanning identity is passed through to all storage and catalog calls — Iceberg Sentry cannot access data the caller cannot access.

### 7.2 Destructive Operation Safety

The orphan file list generated by Iceberg Sentry is a **recommendation**, not an execution. Iceberg Sentry itself never deletes files. The workflow is:

1. Iceberg Sentry generates `orphans-manifest.json` (dry-run by default).
2. Operator reviews the manifest.
3. Operator runs: `iceberg-sentry orphans --confirm` or pipes the manifest to their preferred deletion tool.

Grace period (default 24h) ensures files written during concurrent jobs are never mistakenly flagged.

### 7.3 PII Scanner Safety

- All Parquet reading is done via streaming row group sampling. No column values are persisted.
- Column names and PII type (not values) are the only data written to output.
- The scanner is designed to run without `SELECT` permissions — it reads raw Parquet bytes via storage API, bypassing catalog-level query controls. This requires explicit `--enable-pii-scan` flag and is logged as an audit event.

---

## Page 8: Roadmap

### Phase 1 — The Foundation (Weeks 1–3)

- Go-based Iceberg Metadata Parser (JSON/Avro streaming)
- Storage Abstraction Layer: S3 + HDFS
- Catalog: Hive Metastore + AWS Glue
- Core Health Score: File size, manifest density, snapshot age
- **Delete File Amplification** (first-class, Day 1 feature)
- **Table Format Version Detection**
- CLI output with exit codes 0–5
- JSON and text output formats
- Unit test harness with mock filesystem

### Phase 2 — Advanced Auditing (Weeks 4–6)

- Orphan File Discovery with dry-run and grace period safety controls
- PII Scanner (Parquet row group sampling, regex + entropy)
- Partition Skew Detection with skew coefficient
- Write Pattern Classifier (streaming vs. batch)
- Iceberg REST Catalog support
- `sentry bench` before/after comparison command
- SARIF 2.1.0 output format
- GitHub Actions example workflow
- Integration test suite (MinIO + Hive Metastore containers)
- Branch/tag-aware scanning (`--branch` flag)

### Phase 3 — Cloudera Native & Operationalization (Weeks 7–8)

- Atlas JSON payload export for PII and health findings
- Kerberos / mTLS support for CDP environments
- Prometheus Exporter with Grafana dashboard JSON
- Migration Readiness Audit report template
- Cost Optimization Insights with Snapshot Cost Timeline
- Kubernetes CronJob manifests + Helm chart
- Polaris (Snowflake Open Catalog) support
- Databricks Unity Catalog support (REST compatible)
- Benchmark performance regression tests in CI

### Phase 4 — Community & SaaS (Weeks 9+)

- SaaS MVP (hosted scans, web dashboard)
- Nessie catalog + branch-history analysis
- Slack / PagerDuty alert integrations
- dbt integration (audit Iceberg tables backing dbt models)
- OpenMetadata / DataHub exporter plugins

---

## Page 9: Senior-Level Technical Challenges

These are the engineering problems that distinguish a production-quality tool from a demo:

**1. Scale without OOM (10M+ file tables)**
Solution: When active file count exceeds 500,000, switch Bloom Filter backing from in-memory map to disk-based KV store (bbolt or pebble). Use streaming Avro parsing to avoid loading full manifest files. Benchmark target: < 512MB RAM for 50M file tables.

**2. Point-in-time consistency during concurrent writes**
Solution: Lock the audit to a specific Iceberg snapshot ID at the start of every scan. All subsequent operations (storage crawl, PII sampling, skew analysis) operate against this locked snapshot. In-flight commits after the lock timestamp are invisible to the audit — exactly the guarantee Iceberg's snapshot isolation provides.

**3. PII scanning without data persistence**
Solution: In-memory stream processing using Go's `io.Reader` interface with zero-copy buffers. Parquet footer is parsed first to identify column names and types; only columns with high cardinality string types are sampled. Column values are pattern-matched and immediately discarded — never assigned to a variable that could escape to a log or heap dump.

**4. Accurate delete amplification estimation**
Solution: For each data file, count associated position delete files and equality delete files from the manifest. Estimate read amplification using the Iceberg spec's merge-on-read cost model: `amplification = (data_bytes + delete_bytes) / data_bytes`. This is a conservative estimate; actual amplification depends on engine implementation.

**5. Write pattern classification accuracy**
Solution: Analyse the last N snapshots (default 50) using: commit interval variance, files-per-commit distribution, and manifest-to-file ratios. Streaming tables have high commit frequency, low files-per-commit variance, and high manifest count relative to file count. A naive classifier achieves ~90% accuracy; a configurable `write_pattern` override in `sentry.yaml` handles edge cases.

**6. Multi-cloud cost modelling**
Solution: Abstract cost functions into a pluggable `CostProvider` interface. Default providers for S3 Standard, ADLS Gen2, GCS Standard, and HDFS (user-defined $/GB). Custom providers loadable via YAML config. Estimated costs are always labelled as "estimates based on list-price rates" with a link to the cost model documentation.

---

## Conclusion

Iceberg Sentry is a response to a real and growing operational gap in the Iceberg ecosystem. Every problem it addresses — delete file amplification, metadata bloat, orphan files, untagged PII, partition skew — is documented in production incident reports, Iceberg GitHub issues, and Cloudera community posts. No existing tool addresses all of them in a single, lightweight, engine-agnostic binary with CI/CD-native output.

For Cloudera senior platform engineering audiences specifically, this tool demonstrates: deep knowledge of the Iceberg specification (including v2 delete semantics), production-scale Go engineering (streaming algorithms, Bloom Filters, worker pool concurrency), Cloudera ecosystem fluency (SDX, Ranger, Atlas integration patterns), and operational empathy (dry-run safety, policy-as-code, actionable remediation output).

The `sentry bench` command — measuring health score improvement from 61 to 89 after a compaction run — is the kind of concrete, measurable output that makes a portfolio project memorable.

---

*Iceberg Sentry · github.com/[username]/iceberg-sentry · Apache 2.0 License*
