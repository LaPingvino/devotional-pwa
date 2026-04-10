#!/usr/bin/env python3
"""
Seed inventory_fulltext from the writings table (English prayers with known phelps codes)
and optionally from additional language texts.

Chunks text into <=900-char pieces on word/sentence boundaries for VARCHAR searchability.
"""

import subprocess
import json
import sys
import re

DOLT_DIR = "/home/joop/prayermatching/bahaiwritings"
CHUNK_SIZE = 900
SOURCE_TAG = "bahaiprayers.net/en"


def dolt_query(sql, as_json=True):
    import tempfile, os
    fmt = ["--result-format", "json"] if as_json else []
    with tempfile.NamedTemporaryFile(mode='w', suffix='.sql', delete=False) as f:
        f.write(sql)
        fname = f.name
    try:
        result = subprocess.run(
            ["dolt", "sql"] + fmt + ["--file", fname],
            capture_output=True, text=True, cwd=DOLT_DIR
        )
    finally:
        os.unlink(fname)
    if result.returncode != 0:
        print(f"SQL error: {result.stderr}", file=sys.stderr)
        return None
    if as_json and result.stdout.strip():
        return json.loads(result.stdout)
    return result.stdout


def chunk_text(text, size=CHUNK_SIZE):
    """Split text into chunks of at most `size` chars, preferring sentence/word breaks."""
    text = text.strip()
    chunks = []
    while len(text) > size:
        # Try to break at sentence boundary within last 200 chars of window
        window = text[:size]
        # Prefer breaking after sentence-ending punctuation
        match = None
        for pattern in (r'[.!?]\s+', r'\s+'):
            for m in re.finditer(pattern, window):
                match = m
            if match:
                break
        if match:
            cut = match.end()
        else:
            cut = size
        chunks.append(text[:cut].rstrip())
        text = text[cut:].lstrip()
    if text:
        chunks.append(text)
    return chunks


def escape_sql(s):
    return s.replace("'", "''").replace("\\", "\\\\")


def main():
    # Fetch all prayers with base phelps codes (exclude sub-passage codes like BH00074BLE)
    # Sub-passage codes have 3+ trailing uppercase letters beyond the 7-char base PIN
    rows = dolt_query(
        "SELECT phelps, language, text FROM writings "
        "WHERE source='bahaiprayers.net' AND phelps IS NOT NULL AND phelps <> '' "
        "AND phelps NOT REGEXP '^[A-Z]{2,3}[0-9]{4,5}[A-Z]{3}' "
        "AND text IS NOT NULL AND text <> '' "
        "ORDER BY language, phelps"
    )
    if not rows:
        print("No rows fetched.")
        return

    rows = rows.get("rows", [])
    print(f"Fetched {len(rows)} rows from writings")

    # Check what's already in inventory_fulltext
    existing = dolt_query(
        "SELECT phelps, language FROM inventory_fulltext "
        f"WHERE source='{SOURCE_TAG}'"
    )
    existing_keys = set()
    if existing:
        for r in existing.get("rows", []):
            existing_keys.add((r["phelps"], r["language"]))

    inserts = []
    skipped = 0
    for row in rows:
        phelps = row["phelps"]
        lang = row["language"]
        text = row.get("text", "") or ""
        if not text.strip():
            skipped += 1
            continue
        key = (phelps, lang)
        if key in existing_keys:
            skipped += 1
            continue
        chunks = chunk_text(text)
        for i, chunk in enumerate(chunks):
            inserts.append(
                f"('{escape_sql(phelps)}', '{escape_sql(lang)}', {i}, "
                f"'{escape_sql(chunk)}', '{SOURCE_TAG}')"
            )

    if not inserts:
        print(f"Nothing new to insert (skipped {skipped})")
        return

    print(f"Inserting {len(inserts)} chunks ({skipped} skipped)...")

    # Batch inserts in groups of 100
    batch_size = 100
    for i in range(0, len(inserts), batch_size):
        batch = inserts[i:i + batch_size]
        sql = ("INSERT IGNORE INTO inventory_fulltext (phelps, language, part, text, source) VALUES "
               + ", ".join(batch))
        dolt_query(sql, as_json=False)
        print(f"  Inserted batch {i // batch_size + 1}/{(len(inserts) + batch_size - 1) // batch_size}")

    # Final count
    count = dolt_query("SELECT COUNT(*) as n FROM inventory_fulltext")
    print(f"Done. inventory_fulltext now has {count['rows'][0]['n']} rows")


if __name__ == "__main__":
    main()
