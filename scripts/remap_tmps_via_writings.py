#!/usr/bin/env python3
"""remap_tmps_via_writings.py — For each TMP-coded PBS row, search the
writings table for a non-TMP phelps in the same language whose text
matches the TMP's text. Emit UPDATE pairs for HIGH-confidence matches.

This is the TMP→canonical remap version of match_writings_incipit (which
ran against ORIGINAL scrape residuals, all now PBS-mapped via bulk-TMP).

Outputs SQL to stdout / file. Apply via dolt sql.
"""
import argparse, json, re, subprocess, sys, uuid
from pathlib import Path

DOLT_DIR = Path.home() / "bahaiwritings"

# Reuse stopwords + helpers from match_writings_incipit
sys.path.insert(0, str(Path(__file__).resolve().parent))
from match_writings_incipit import (
    STOPWORDS, LANG_ALIASES, lang_clause, normalize_first_words, dolt_query,
)


def fix_dropcap_glue(text):
    if not text:
        return text
    return re.sub(r"^([IOA])([a-z])", r"\1 \2", text)


def search_phelps(lang, incipit_words, exclude_phelps_prefixes):
    if len(incipit_words.split()) < 4:
        return []
    stop = STOPWORDS.get(lang, STOPWORDS["en"])
    words = [w for w in incipit_words.split() if w not in stop][:6]
    if len(words) < 3:
        words = incipit_words.split()[:6]
    if not words:
        return []
    clauses = " AND ".join(
        f"LOWER(text) LIKE '%{w}%'" for w in (w.replace("'", "''") for w in words)
    )
    sql = (f"SELECT phelps, source, source_id, LEFT(text, 250) AS head, LENGTH(text) AS tlen "
           f"FROM writings WHERE {lang_clause(lang)} AND phelps IS NOT NULL "
           f"AND phelps NOT LIKE 'TMP%' "
           f"AND {clauses} LIMIT 12")
    return dolt_query(sql)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lang", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    # Pre-fetch existing PBS rows (cat, phelps_code) to avoid PK collisions
    pbs_rows = dolt_query(
        f"SELECT category_name, phelps_code FROM prayer_book_structure "
        f"WHERE source_language='{args.lang}:bpapp'"
    )
    existing_keys = {(r["category_name"], r["phelps_code"]) for r in pbs_rows}

    # Get TMP rows with their text
    tmp_rows = dolt_query(
        f"SELECT pbs.phelps_code, pbs.category_name, w.text, LENGTH(w.text) AS tlen "
        f"FROM prayer_book_structure pbs "
        f"JOIN writings w ON pbs.phelps_code=w.phelps AND w.language='{args.lang}' "
        f"WHERE pbs.source_language='{args.lang}:bpapp' "
        f"AND pbs.phelps_code LIKE 'TMP%'"
    )
    if not tmp_rows:
        print(f"[{args.lang}] no TMPs to process", file=sys.stderr)
        return

    out = open(args.out, "w")
    out.write(f"-- TMP remap via writings-incipit search for {args.lang}:bpapp\n")
    out.write("SET FOREIGN_KEY_CHECKS=0;\n\n")

    n_high = n_med = n_low = n_dup = 0
    for r in tmp_rows:
        tmp_phelps = r["phelps_code"]
        cat = r.get("category_name", "")
        text = fix_dropcap_glue(r.get("text", "") or "")
        bpapp_len = r.get("tlen", 0) or 0
        incipit = normalize_first_words(text, 10)
        if not incipit or len(incipit.split()) < 4:
            out.write(f"-- SKIP {tmp_phelps}: no incipit\n")
            continue

        cands = search_phelps(args.lang, incipit, [])
        # Rank candidates with the same proximity + length-disambig logic
        inc_words = [w for w in incipit.split() if len(w) > 2][:10]
        ranked = []
        for c in cands:
            head = (c.get("head", "") or "").lower()[:500]
            pos = 0; hit = 0; last = -1
            for w in inc_words:
                idx = head.find(w, pos)
                if idx >= 0:
                    hit += 1; last = idx; pos = idx + len(w)
            score = hit / max(1, len(inc_words))
            if hit == len(inc_words) and last < 250:
                score += 0.3
            tlen = c.get("tlen", 0) or 0
            if tlen and bpapp_len:
                ratio = max(tlen, bpapp_len) / max(min(tlen, bpapp_len), 1)
                if ratio > 3:
                    score -= 0.25
            ranked.append((score, c))
        ranked.sort(key=lambda x: -x[0])

        if not ranked:
            out.write(f"-- NO MATCH {tmp_phelps} ({cat[:40]}): {incipit[:60]}\n")
            continue

        top_score, top_c = ranked[0]
        target = top_c["phelps"]
        confidence = "HIGH" if top_score >= 0.9 else ("MED" if top_score >= 0.7 else "LOW")

        # PK conflict check
        if (cat, target) in existing_keys:
            out.write(f"-- DUP-TARGET {tmp_phelps} → {target} (already in {cat[:30]}) — DELETE source\n")
            out.write(f"DELETE FROM prayer_book_structure WHERE source_language='{args.lang}:bpapp' AND phelps_code='{tmp_phelps}';\n")
            out.write(f"DELETE FROM writings WHERE phelps='{tmp_phelps}' AND language='{args.lang}';\n")
            n_dup += 1
            continue

        if confidence == "HIGH":
            out.write(f"\n-- {tmp_phelps} → {target} (score {top_score:.2f}, {cat[:40]})\n")
            out.write(f"UPDATE writings SET phelps='{target}' WHERE phelps='{tmp_phelps}' AND language='{args.lang}';\n")
            out.write(f"UPDATE prayer_book_structure SET phelps_code='{target}', "
                      f"notes='writings-incipit-remap; was {tmp_phelps}; HIGH score={top_score:.2f}' "
                      f"WHERE source_language='{args.lang}:bpapp' AND phelps_code='{tmp_phelps}';\n")
            existing_keys.add((cat, target))
            n_high += 1
        elif confidence == "MED":
            out.write(f"-- MED {tmp_phelps} → {target} score={top_score:.2f}: {incipit[:50]}\n")
            n_med += 1
        else:
            out.write(f"-- LOW {tmp_phelps} → {target} score={top_score:.2f}: {incipit[:50]}\n")
            n_low += 1

    out.write("\nSET FOREIGN_KEY_CHECKS=1;\n")
    out.close()
    print(f"[{args.lang}] HIGH={n_high} MED={n_med} LOW={n_low} DUP-DEL={n_dup}")


if __name__ == "__main__":
    main()
