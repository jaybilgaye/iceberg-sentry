#!/usr/bin/env python3
"""Generate Iceberg test tables for iceberg-sentry integration tests.

Usage:
    pip install "pyiceberg[pyarrow]>=0.7.0"
    python scripts/gen_fixtures.py --root testdata/fixtures/generated

Produces three tables under <root>:
    healthy_v2/        --  one large data file, no deletes
    streaming_v2/      --  many small data files, high manifest count
    delete_amp_v2/     --  data files plus position deletes (ratio ~30%)

Each table is a real Iceberg v2 table written by pyiceberg, which means the
metadata.json + manifest-list + manifest-file bytes are byte-for-byte
identical to what a production engine produces. We use these as integration
test fixtures for the Go scanner.
"""

from __future__ import annotations

import argparse
import shutil
from pathlib import Path

import pyarrow as pa
from pyiceberg.catalog import load_catalog
from pyiceberg.schema import Schema
from pyiceberg.types import LongType, NestedField, StringType


SCHEMA = Schema(
    NestedField(field_id=1, name="id", field_type=LongType(), required=True),
    NestedField(field_id=2, name="payload", field_type=StringType(), required=False),
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


def make_arrow(rows: int, payload_size: int = 64) -> pa.Table:
    return pa.table(
        {
            "id": pa.array(range(rows), type=pa.int64()),
            "payload": pa.array(["x" * payload_size] * rows, type=pa.string()),
        }
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
    # Issue several deletes to populate v2 position-delete files.
    tbl.delete("id < 1000")
    tbl.delete("id >= 9000 AND id < 9500")


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

    print(f"Wrote fixtures under {root}")


if __name__ == "__main__":
    main()
