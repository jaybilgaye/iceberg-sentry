#!/usr/bin/env python3
"""Generate Iceberg test tables for iceberg-sentry integration tests.

Usage:
    pip install "pyiceberg[pyarrow]>=0.7.0"
    python scripts/gen_fixtures.py --root testdata/fixtures/generated

Produces these tables under <root>:
    healthy_v2/        --  one large data file, no deletes
    streaming_v2/      --  many small data files, high manifest count
    delete_amp_v2/     --  data files plus position deletes (ratio ~30%)
    skewed_v2/         --  date-partitioned table with severe data skew
    pii_v2/            --  table containing email + credit-card columns

Each table is a real Iceberg v2 table written by pyiceberg, which means the
metadata.json + manifest-list + manifest-file bytes are byte-for-byte
identical to what a production engine produces. We use these as integration
test fixtures for the Go scanner.
"""

from __future__ import annotations

import argparse
import datetime
import random
import shutil
from pathlib import Path

import pyarrow as pa
from pyiceberg.catalog import load_catalog
from pyiceberg.partitioning import PartitionField, PartitionSpec
from pyiceberg.schema import Schema
from pyiceberg.transforms import DayTransform
from pyiceberg.types import (
    DateType,
    LongType,
    NestedField,
    StringType,
    TimestampType,
)


SCHEMA = Schema(
    NestedField(field_id=1, name="id", field_type=LongType(), required=True),
    NestedField(field_id=2, name="payload", field_type=StringType(), required=False),
)

PARTITIONED_SCHEMA = Schema(
    NestedField(field_id=1, name="id", field_type=LongType(), required=True),
    NestedField(field_id=2, name="ts", field_type=TimestampType(), required=True),
    NestedField(field_id=3, name="region", field_type=StringType(), required=False),
)

PII_SCHEMA = Schema(
    NestedField(field_id=1, name="customer_id", field_type=LongType(), required=True),
    NestedField(field_id=2, name="email", field_type=StringType(), required=False),
    NestedField(field_id=3, name="card", field_type=StringType(), required=False),
    NestedField(field_id=4, name="note", field_type=StringType(), required=False),
)


def make_catalog(root: Path):
    root.mkdir(parents=True, exist_ok=True)
    return load_catalog(
        "fixtures",
        **{
            "type": "sql",
            "uri": f"sqlite:///{root}/catalog.db",
            "warehouse": str(root),
        },
    )


_ARROW_SCHEMA = pa.schema([
    pa.field("id", pa.int64(), nullable=False),
    pa.field("payload", pa.string(), nullable=True),
])


def make_arrow(rows: int, payload_size: int = 64) -> pa.Table:
    return pa.table(
        {
            "id": pa.array(range(rows), type=pa.int64()),
            "payload": pa.array(["x" * payload_size] * rows, type=pa.string()),
        },
        schema=_ARROW_SCHEMA,
    )


def healthy_v2(catalog, root: Path):
    if catalog.table_exists("fixtures.healthy_v2"):
        catalog.drop_table("fixtures.healthy_v2")
    tbl = catalog.create_table("fixtures.healthy_v2", schema=SCHEMA)
    tbl.append(make_arrow(rows=200_000, payload_size=512))


def streaming_v2(catalog, root: Path):
    if catalog.table_exists("fixtures.streaming_v2"):
        catalog.drop_table("fixtures.streaming_v2")
    tbl = catalog.create_table("fixtures.streaming_v2", schema=SCHEMA)
    for batch in range(50):
        tbl.append(make_arrow(rows=200))


def delete_amp_v2(catalog, root: Path):
    if catalog.table_exists("fixtures.delete_amp_v2"):
        catalog.drop_table("fixtures.delete_amp_v2")
    tbl = catalog.create_table("fixtures.delete_amp_v2", schema=SCHEMA)
    tbl.append(make_arrow(rows=10_000))
    tbl.delete("id < 1000")
    tbl.delete("id >= 9000 AND id < 9500")


_PART_SCHEMA = pa.schema([
    pa.field("id", pa.int64(), nullable=False),
    pa.field("ts", pa.timestamp("us"), nullable=False),
    pa.field("region", pa.string(), nullable=True),
])


def skewed_v2(catalog, root: Path):
    """Date-partitioned table with one massive partition and several tiny ones."""
    if catalog.table_exists("fixtures.skewed_v2"):
        catalog.drop_table("fixtures.skewed_v2")
    spec = PartitionSpec(
        PartitionField(source_id=2, field_id=1000, transform=DayTransform(), name="ts_day"),
    )
    tbl = catalog.create_table("fixtures.skewed_v2", schema=PARTITIONED_SCHEMA, partition_spec=spec)
    base = datetime.datetime(2026, 1, 1)
    # Hot partition: 100k rows on day 0
    hot = pa.table({
        "id": pa.array(range(100_000), type=pa.int64()),
        "ts": pa.array([base] * 100_000, type=pa.timestamp("us")),
        "region": pa.array(["us"] * 100_000, type=pa.string()),
    }, schema=_PART_SCHEMA)
    tbl.append(hot)
    # Sparse partitions: 100 rows per day across 6 subsequent days
    for day in range(1, 7):
        ts = base + datetime.timedelta(days=day)
        cold = pa.table({
            "id": pa.array(range(100), type=pa.int64()),
            "ts": pa.array([ts] * 100, type=pa.timestamp("us")),
            "region": pa.array(["eu"] * 100, type=pa.string()),
        }, schema=_PART_SCHEMA)
        tbl.append(cold)


_PII_SCHEMA = pa.schema([
    pa.field("customer_id", pa.int64(), nullable=False),
    pa.field("email", pa.string(), nullable=True),
    pa.field("card", pa.string(), nullable=True),
    pa.field("note", pa.string(), nullable=True),
])


def pii_v2(catalog, root: Path):
    if catalog.table_exists("fixtures.pii_v2"):
        catalog.drop_table("fixtures.pii_v2")
    tbl = catalog.create_table("fixtures.pii_v2", schema=PII_SCHEMA)
    rng = random.Random(42)
    rows = 5_000
    emails = [f"user{i:05d}@example.com" for i in range(rows)]
    cards = ["4111 1111 1111 1111"] * rows
    notes = ["just a normal product review" for _ in range(rows)]
    for _ in range(20):
        notes[rng.randrange(rows)] = "AKIAIOSFODNN7EXAMPLEpQRsTuVwXyZ123456abcd"
    tbl.append(pa.table({
        "customer_id": pa.array(range(rows), type=pa.int64()),
        "email": pa.array(emails, type=pa.string()),
        "card": pa.array(cards, type=pa.string()),
        "note": pa.array(notes, type=pa.string()),
    }, schema=_PII_SCHEMA))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default="testdata/fixtures/generated")
    ap.add_argument("--clean", action="store_true", help="delete --root before generating")
    args = ap.parse_args()

    root = Path(args.root).resolve()
    if args.clean and root.exists():
        shutil.rmtree(root)

    catalog = make_catalog(root)
    if "fixtures" not in (ns[0] for ns in catalog.list_namespaces()):
        catalog.create_namespace("fixtures")

    healthy_v2(catalog, root)
    streaming_v2(catalog, root)
    delete_amp_v2(catalog, root)
    skewed_v2(catalog, root)
    pii_v2(catalog, root)

    print(f"Wrote fixtures under {root}")


if __name__ == "__main__":
    main()
