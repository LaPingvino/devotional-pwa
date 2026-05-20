#!/usr/bin/env python3
"""Import major missing writings from inventory_fulltext → writings table.

Sources covered:
  swb   = Selections from the Writings of the Báb           (bahai.org/bab)
  swab  = Selections from the Writings of 'Abdu'l-Bahá      (bahai.org/ab-selections)
  pup   = The Promulgation of Universal Peace               (bahai.org/pup)
  gems  = Gems of Divine Mysteries                          (bahai.org/bah-gems)

For each row in inventory_fulltext under the corresponding source, emit one row
in the writings table with an 11-char extended phelps:
   <7-char base PIN> + LPAD(part+1, 4, '0')

The `name` field is set to the tablet/address title (from inventory.Title).
This lets gen_hugo_data.go group entries by base code into books via
writingBaseCode().

Also writes the corresponding i18n metadata rows so gen_hugo_data.go picks
up the new types automatically.

Usage:
  python3 scripts/import_new_writings.py [--dry-run]
"""
import argparse
import json
import subprocess
import sys

DOLT_DIR = "/home/joop/bahaiwritings"

# (db_type, source, author, author_prefix, title, order, single_book)
TYPES = [
    ("swb",  "bahai.org/bab",           "The Báb",        "BB", "Selections from the Writings of the Báb",       13, False),
    ("swab", "bahai.org/ab-selections", "'Abdu'l-Bahá",   "AB", "Selections from the Writings of 'Abdu'l-Bahá", 14, False),
    ("pup",  "bahai.org/pup",           "'Abdu'l-Bahá",   "AB", "The Promulgation of Universal Peace",          15, False),
    ("gems", "bahai.org/bah-gems",      "Bahá'u'lláh",    "BH", "Gems of Divine Mysteries",                     16, True),
]


def dolt(query, dry_run=False):
    if dry_run:
        print(f"[dry-run] {query[:120]}{'…' if len(query) > 120 else ''}")
        return ""
    # Use stdin for the query so we don't hit argv length limits on big INSERTs
    res = subprocess.run(
        ["dolt", "sql", "--result-format", "csv"],
        cwd=DOLT_DIR, capture_output=True, text=True, input=query,
    )
    if res.returncode != 0:
        sys.stderr.write(f"DOLT ERROR: {res.stderr}\nQUERY: {query[:200]}\n")
        sys.exit(1)
    return res.stdout


def get_titles(pins):
    """Return dict pin → title from inventory."""
    if not pins:
        return {}
    qpins = ",".join(f"'{p}'" for p in pins)
    out = dolt(f"SELECT PIN, Title FROM inventory WHERE PIN IN ({qpins})")
    lines = out.strip().splitlines()[1:]
    titles = {}
    for line in lines:
        # CSV parsing — Title may contain commas/quotes
        import csv, io
        for row in csv.reader(io.StringIO(line)):
            if len(row) >= 2:
                titles[row[0]] = row[1]
    return titles


def sql_escape(s):
    return s.replace("\\", "\\\\").replace("'", "''")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--only", help="Only import this db_type (e.g. swb)")
    args = ap.parse_args()

    for db_type, source, author, prefix, title, order, single_book in TYPES:
        if args.only and args.only != db_type:
            continue
        print(f"\n=== {db_type} ({source}) ===")

        # Fetch all parts for this source
        out = dolt(
            f"SELECT phelps, language, part, text FROM inventory_fulltext "
            f"WHERE source='{source}' ORDER BY phelps, language, part"
        )
        lines = out.strip().splitlines()
        if len(lines) < 2:
            print(f"  no rows for source {source}, skipping")
            continue

        import csv, io
        rdr = csv.reader(io.StringIO(out))
        rows = list(rdr)[1:]  # skip header
        print(f"  {len(rows)} parts in inventory_fulltext")

        # Collect base PINs to fetch titles
        pins = sorted({r[0] for r in rows})
        titles = get_titles(pins)
        print(f"  {len(pins)} distinct tablets, {len(titles)} with titles in inventory")

        # Emit INSERTs (batched)
        # writings schema: phelps, language, version, name, type, text, source, source_id, is_verified
        # version is PRI with default 'uuid()' — leave it default by omitting
        insert_sql = (
            "INSERT INTO writings (version, phelps, language, name, type, text, source, source_id, is_verified) VALUES\n"
        )
        values = []
        for base_pin, language, part_str, text in rows:
            part = int(part_str)
            # 11-char extended phelps: 7-char base + 4-digit suffix
            ext = f"{base_pin}{part + 1:04d}"
            name = titles.get(base_pin, "")
            text_html = f"<p>{text}</p>"
            # Deterministic primary key: type:phelps:language
            version = f"{db_type}:{ext}:{language}"
            values.append(
                f"('{version}','{ext}','{language}','{sql_escape(name)}','{db_type}','{sql_escape(text_html)}',"
                f"'{source}','{base_pin}',1)"
            )

        # Batch in chunks of 500 for dolt sql length safety
        BATCH = 500
        total = len(values)
        for i in range(0, total, BATCH):
            chunk = values[i:i + BATCH]
            stmt = insert_sql + ",\n".join(chunk) + ";"
            if args.dry_run:
                print(f"  [dry-run] would insert rows {i}–{i + len(chunk)} ({len(chunk)} rows)")
            else:
                dolt("SET FOREIGN_KEY_CHECKS=0;" + stmt + "SET FOREIGN_KEY_CHECKS=1;")
                print(f"  inserted rows {i}–{i + len(chunk)} ({len(chunk)} rows)")

        # i18n metadata
        flags = {
            "single_book": single_book,
            "show_names": True,
            "split_paras": False,
        }
        i18n_val = {
            "author": author,
            "author_prefix": prefix,
            "db_type": db_type,
            "flags": flags,
            "order": order,
            "title": title,
        }
        # Properly JSON-encode then escape for SQL
        val_json = json.dumps(i18n_val, ensure_ascii=False)
        val_sql = sql_escape(val_json)
        i18n_sql = (
            f"INSERT INTO i18n (`key`, language, value) VALUES "
            f"('writings/{db_type}', 'en', '{val_sql}') "
            f"ON DUPLICATE KEY UPDATE value=VALUES(value);"
        )
        if args.dry_run:
            print(f"  [dry-run] i18n row: writings/{db_type}")
        else:
            dolt(i18n_sql)
            print(f"  wrote i18n: writings/{db_type}")

    print("\nDone.")


if __name__ == "__main__":
    main()
