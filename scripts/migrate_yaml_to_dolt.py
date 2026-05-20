#!/usr/bin/env python3
"""Migrate UI translations from i18n/*.yaml into the Dolt i18n table.

Each YAML key K in <lang>.yaml becomes a row:
  i18n(`key`='ui/K', language=<lang>, value=JSON_QUOTE(<string>))

The i18n.value column is JSON; storing UI strings as quoted JSON scalars
keeps the schema uniform with the existing writings/<key> and author/<prefix>
entries. Consumers retrieve via JSON_UNQUOTE(value).

After migration, gen_i18n.go can read from Dolt as the source of truth
and write YAML / static/data/i18n.json as build artifacts.

Run:
  python3 scripts/migrate_yaml_to_dolt.py [--dry-run] [--purge]
    --purge: also delete existing ui/* rows before inserting (use after
             schema or naming changes; harmless on first run)
"""
import argparse
import glob
import json
import os
import re
import subprocess
import sys

DOLT_DIR = "/home/joop/bahaiwritings"
YAML_DIR = os.path.join(os.path.dirname(__file__), "..", "i18n")


def parse_yaml(path):
    """Lightweight YAML parser for the simple key: \"value\" format these
    files use. Avoids pulling in PyYAML as a dependency.
    """
    out = {}
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.rstrip("\n")
            # Strip full-line comments and blank lines
            if not line.strip() or line.lstrip().startswith("#"):
                continue
            m = re.match(r'^([A-Za-z0-9_]+):\s*"((?:[^"\\]|\\.)*)"\s*(?:#.*)?$', line)
            if m:
                key, val = m.group(1), m.group(2)
                val = (val.replace('\\"', '"').replace('\\n', '\n')
                          .replace('\\t', '\t').replace('\\\\', '\\'))
                out[key] = val
                continue
            # Tolerate unquoted scalar values
            m = re.match(r'^([A-Za-z0-9_]+):\s*([^"#].*?)\s*$', line)
            if m:
                out[m.group(1)] = m.group(2).strip()
    return out


def dolt(query):
    res = subprocess.run(
        ["dolt", "sql", "--result-format", "csv"],
        cwd=DOLT_DIR, capture_output=True, text=True, input=query,
    )
    if res.returncode != 0:
        sys.stderr.write(f"DOLT ERROR: {res.stderr[:1500]}\nQUERY: {query[:400]}\n")
        sys.exit(1)
    return res.stdout


def sql_escape(s):
    return s.replace("\\", "\\\\").replace("'", "''")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--purge", action="store_true",
                    help="DELETE existing ui/* rows before inserting")
    args = ap.parse_args()

    yaml_files = sorted(glob.glob(os.path.join(YAML_DIR, "*.yaml")))
    if not yaml_files:
        sys.exit(f"no yaml files in {YAML_DIR}")

    if args.purge and not args.dry_run:
        print("Purging existing ui/* rows…")
        dolt("DELETE FROM i18n WHERE `key` LIKE 'ui/%';")

    total_rows = 0
    skipped_dup = 0
    for path in yaml_files:
        lang = os.path.splitext(os.path.basename(path))[0]
        kvs = parse_yaml(path)
        if not kvs:
            print(f"  {lang}: 0 strings (skipped)")
            continue

        values = []
        for k, v in kvs.items():
            if not v:
                continue
            # Value is stored as JSON scalar (a JSON string literal)
            json_val = json.dumps(v, ensure_ascii=False)
            values.append(
                f"('ui/{sql_escape(k)}','{sql_escape(lang)}','{sql_escape(json_val)}')"
            )
        if not values:
            continue

        print(f"  {lang}: {len(values)} strings")
        if args.dry_run:
            total_rows += len(values)
            continue

        # Insert in chunks of ~200 with ON DUPLICATE update so re-runs are idempotent
        BATCH = 200
        for i in range(0, len(values), BATCH):
            chunk = values[i:i + BATCH]
            stmt = (
                "INSERT INTO i18n (`key`, language, value) VALUES "
                + ",".join(chunk)
                + " ON DUPLICATE KEY UPDATE value=VALUES(value);"
            )
            dolt(stmt)
        total_rows += len(values)

    print(f"\nTotal rows written: {total_rows}")
    if args.dry_run:
        return

    # Quick verification
    n = dolt("SELECT COUNT(*) FROM i18n WHERE `key` LIKE 'ui/%';")
    print(f"i18n ui/* rows now in Dolt: {n.strip().splitlines()[-1]}")


if __name__ == "__main__":
    main()
