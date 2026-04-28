#!/usr/bin/env python3
"""ar_strict_match.py — Tight Arabic TMP matcher. AND-on-words is too
permissive for Arabic (highly formulaic prayers all share الله/إلهي/ربي).

Approach:
1. Normalize Arabic text aggressively (strip diacritics, alef forms, ya, ta-marbuta, hamza variants, whitespace, common prefix patterns "هو الله", "بسم الله").
2. For each TMP, take first 60 normalized chars as "fingerprint prefix".
3. Search for any non-TMP ar writings row whose normalized text contains
   that exact fingerprint substring AND whose length is within 1.5x.
4. HIGH = fingerprint at start (within first 80 chars). MED = fingerprint
   somewhere in first 300 chars.

Emits SQL.
"""
import argparse, re, sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from match_writings_incipit import dolt_query


# Arabic diacritics (harakat + tatweel)
DIAC = re.compile(r"[ؐ-ًؚ-ٰٟۖ-ۜ۟-۪ۨ-ۭـ]")
# Drop-cap glue: leading single-letter run followed by lowercase (not Arabic-specific)


def normalize_ar(text):
    if not text:
        return ""
    t = DIAC.sub("", text)
    # Unify alef variants
    t = t.replace("أ", "ا").replace("إ", "ا").replace("آ", "ا").replace("ٱ", "ا")
    # Unify ya
    t = t.replace("ى", "ي").replace("ئ", "ي")
    # Unify ta-marbuta
    t = t.replace("ة", "ه")
    # Unify waw with hamza
    t = t.replace("ؤ", "و")
    # Strip HTML
    t = re.sub(r"<[^>]+>", " ", t)
    # Strip markdown headings
    t = re.sub(r"^#+\s+[^\n]*\n+", "", t, flags=re.M)
    # Strip common invocations
    invocs = [
        r"^هو الله[^\n.!]*[.!\n]\s*",
        r"^بسم الله[^\n.!]*[.!\n]\s*",
        r"^هو ا?لاقدس[^\n.!]*[.!\n]\s*",
        r"^هو ا?لابهي[^\n.!]*[.!\n]\s*",
    ]
    for p in invocs:
        t = re.sub(p, "", t, count=1)
    # Collapse whitespace
    t = re.sub(r"\s+", " ", t).strip()
    return t


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    # Fetch all ar non-TMP writings
    rows_real = dolt_query(
        "SELECT phelps, text, LENGTH(text) AS tlen FROM writings "
        "WHERE language='ar' AND phelps IS NOT NULL AND phelps NOT LIKE 'TMP%'"
    )
    real = []
    for r in rows_real:
        norm = normalize_ar(r.get("text", "") or "")
        if len(norm) < 30:
            continue
        real.append({"phelps": r["phelps"], "norm": norm, "tlen": r.get("tlen", 0) or 0})
    print(f"[ar] indexed {len(real)} real writings", file=sys.stderr)

    # Fetch all ar TMP writings (with PBS info for output context)
    rows_tmp = dolt_query(
        "SELECT w.phelps, w.text, LENGTH(w.text) AS tlen "
        "FROM writings w WHERE w.language='ar' AND w.phelps LIKE 'TMP%'"
    )
    print(f"[ar] {len(rows_tmp)} TMPs to match", file=sys.stderr)

    out = open(args.out, "w")
    out.write("-- ar strict matcher (normalize+fingerprint-prefix)\n")
    out.write("SET FOREIGN_KEY_CHECKS=0;\n\n")
    n_high = n_med = n_none = 0
    for tmp in rows_tmp:
        tmp_ph = tmp["phelps"]
        tmp_norm = normalize_ar(tmp.get("text", "") or "")
        tmp_len = tmp.get("tlen", 0) or 0
        if len(tmp_norm) < 30:
            out.write(f"-- SKIP {tmp_ph}: too short after normalize\n")
            continue
        fp = tmp_norm[:60]
        candidates = []
        for r in real:
            if fp in r["norm"]:
                pos = r["norm"].find(fp)
                # length sanity
                ratio = max(r["tlen"], tmp_len) / max(min(r["tlen"], tmp_len), 1)
                candidates.append((pos, ratio, r["phelps"]))
        if not candidates:
            # Try shorter fingerprint
            fp40 = tmp_norm[:40]
            for r in real:
                if fp40 in r["norm"][:200]:
                    pos = r["norm"].find(fp40)
                    ratio = max(r["tlen"], tmp_len) / max(min(r["tlen"], tmp_len), 1)
                    if ratio < 2.0:
                        candidates.append((pos, ratio, r["phelps"]))
        if not candidates:
            out.write(f"-- NO MATCH {tmp_ph}: {tmp_norm[:80]}\n")
            n_none += 1
            continue
        candidates.sort(key=lambda x: (x[0], x[1]))
        pos, ratio, target = candidates[0]
        confidence = "HIGH" if pos < 80 and ratio < 1.5 else ("MED" if pos < 300 and ratio < 2.0 else "LOW")
        if confidence == "HIGH":
            out.write(f"-- HIGH {tmp_ph} -> {target} (pos={pos}, ratio={ratio:.2f}): {tmp_norm[:60]}\n")
            out.write(f"UPDATE writings SET phelps='{target}' WHERE phelps='{tmp_ph}' AND language='ar';\n")
            out.write(f"UPDATE prayer_book_structure SET phelps_code='{target}', notes='ar-strict; was {tmp_ph}' WHERE phelps_code='{tmp_ph}';\n\n")
            n_high += 1
        elif confidence == "MED":
            out.write(f"-- MED  {tmp_ph} -> {target} (pos={pos}, ratio={ratio:.2f}): {tmp_norm[:60]}\n")
            n_med += 1
        else:
            out.write(f"-- LOW  {tmp_ph} -> {target} (pos={pos}, ratio={ratio:.2f}): {tmp_norm[:60]}\n")

    out.write("\nSET FOREIGN_KEY_CHECKS=1;\n")
    out.close()
    print(f"[ar] HIGH={n_high} MED={n_med} NONE={n_none}")


if __name__ == "__main__":
    main()
