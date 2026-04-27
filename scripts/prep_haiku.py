#!/usr/bin/env python3
"""prep_haiku.py — Build self-contained review packets for Haiku agents.

For each language's bpapp_<L>_review.tsv, fetch:
  1. the bpapp prayer's full text from the cache
  2. the top_phelps and runner_up writings text from dolt

Emit a flat JSONL (one row per prayer to review) so the agent only needs
to Read+Write — no Bash or DB access.

Usage:
  python3 scripts/prep_haiku.py --langs ar,en,de,…   # explicit list
  python3 scripts/prep_haiku.py --all                # everything with >5 review rows
"""
import argparse
import csv
import json
import os
import subprocess
import sys
from pathlib import Path

DOLT_DIR = os.path.expanduser("~/bahaiwritings")
ROOT = Path(__file__).resolve().parent.parent  # devotional-pwa


def dolt_query(sql):
    """Run dolt sql -q with CSV output, return list of dicts."""
    raw = subprocess.run(
        ["dolt", "sql", "-q", sql, "-r", "csv"],
        cwd=DOLT_DIR, capture_output=True, check=True,
    ).stdout
    out = raw.decode("utf-8", errors="replace")
    rdr = csv.DictReader(out.splitlines())
    return list(rdr)


def fetch_writings(lang, phelps_codes):
    """Bulk-fetch writings.text for a set of phelps codes in a language.
    Returns dict: phelps -> [{source, source_id, text}, ...]"""
    if not phelps_codes:
        return {}
    # Dolt csv handles quoting reasonably; clamp text to 1500 chars.
    in_clause = ",".join("'" + p.replace("'", "''") + "'" for p in phelps_codes)
    # zh aliases both Hans and Hant
    lang_clause = (
        "language IN ('zh-Hans','zh-Hant')" if lang == "zh"
        else f"language = '{lang}'"
    )
    sql = (
        f"SELECT phelps, source, source_id, "
        f"REPLACE(REPLACE(SUBSTRING(text, 1, 1500), CHAR(10), ' '), CHAR(13), ' ') AS text "
        f"FROM writings "
        f"WHERE phelps IN ({in_clause}) AND {lang_clause}"
    )
    rows = dolt_query(sql)
    out = {}
    for r in rows:
        out.setdefault(r["phelps"], []).append({
            "source": r["source"],
            "source_id": r["source_id"],
            "text": r["text"][:800],  # trim more for context size
        })
    return out


def load_cache_full(lang, bpapp_id):
    p = ROOT / "bpapp_cache" / lang / f"prayer_{bpapp_id}.json"
    if not p.exists():
        return ""
    try:
        return json.loads(p.read_text())["full_text"][:1200]
    except Exception:
        return ""


def process_lang(lang):
    review_path = ROOT / f"bpapp_{lang}_review.tsv"
    if not review_path.exists():
        print(f"[{lang}] no review file")
        return
    with review_path.open(encoding="utf-8", errors="replace") as f:
        rows = list(csv.DictReader(f, delimiter="\t"))
    if not rows:
        print(f"[{lang}] empty review")
        return

    # Collect all unique phelps to bulk-fetch
    needed = set()
    for r in rows:
        if r["top_phelps"]: needed.add(r["top_phelps"])
        if r["runner_up"]: needed.add(r["runner_up"])
    print(f"[{lang}] {len(rows)} review rows, fetching {len(needed)} writings…", file=sys.stderr)
    writings = fetch_writings(lang, list(needed))

    out_path = ROOT / f"bpapp_{lang}_haiku_input.jsonl"
    out_path.parent.mkdir(exist_ok=True)
    n_written = 0
    with out_path.open("w") as out:
        for r in rows:
            bpapp_full = load_cache_full(lang, r["bpapp_id"])
            packet = {
                "bpapp_id": r["bpapp_id"],
                "category": r["category"],
                "bpapp_title": r["title"],
                "bpapp_text": bpapp_full or r["start_text"],
                "candidates": [],
            }
            for label, ph_key, sc_key in (("top", "top_phelps", "top_score"),
                                           ("runner", "runner_up", "runner_score")):
                ph = r[ph_key]
                if not ph or ph not in writings:
                    continue
                for w in writings[ph]:
                    packet["candidates"].append({
                        "label": label,
                        "phelps": ph,
                        "source": w["source"],
                        "score": float(r[sc_key] or 0),
                        "text": w["text"],
                    })
            if not packet["candidates"]:
                # No candidates at all — skip; will be handled as TMP later
                continue
            out.write(json.dumps(packet, ensure_ascii=False) + "\n")
            n_written += 1
    print(f"[{lang}] wrote {out_path} with {n_written}/{len(rows)} packets")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--langs", help="comma-separated lang codes")
    ap.add_argument("--all", action="store_true", help="all langs with review.tsv")
    args = ap.parse_args()
    if args.all:
        langs = sorted(p.stem.replace("bpapp_", "").replace("_review", "")
                       for p in ROOT.glob("bpapp_*_review.tsv"))
    elif args.langs:
        langs = args.langs.split(",")
    else:
        ap.error("specify --langs or --all")
    for L in langs:
        try:
            process_lang(L)
        except Exception as e:
            print(f"[{L}] ERROR: {e}", file=sys.stderr)


if __name__ == "__main__":
    main()
