#!/usr/bin/env python3
"""cross_source_match.py — For a given language, find prayers that exist
in BOTH :bpapp and :bpnet prayerbooks but under DIFFERENT phelps codes.

This catches mis-coded prayers where the same text was coded one way in
bpnet and differently in bpapp, including cases where one side has a TMP
and the other has a real phelps. Identifying these lets us:
1. Reconcile (pick the canonical phelps)
2. Surface the cross-source variant as alt_sources

Method: AND-on-words proximity match between bpapp and bpnet writings
for the same language, where phelps_codes differ. Length comparison
disambiguates parent vs excerpt.
"""
import argparse, json, re, subprocess, sys
from pathlib import Path

DOLT_DIR = Path.home() / "bahaiwritings"

sys.path.insert(0, str(Path(__file__).resolve().parent))
from match_writings_incipit import (
    STOPWORDS, normalize_first_words, dolt_query,
)


def fetch_book_prayers(lang, book_suffix):
    """Get all phelps + first 200 chars of text for prayers in lang:book_suffix."""
    sl = f"{lang}:{book_suffix}"
    rows = dolt_query(
        f"SELECT DISTINCT pbs.phelps_code, w.text, LENGTH(w.text) AS tlen "
        f"FROM prayer_book_structure pbs "
        f"JOIN writings w ON pbs.phelps_code=w.phelps AND w.language='{lang}' "
        f"WHERE pbs.source_language='{sl}' AND pbs.phelps_code IS NOT NULL"
    )
    out = []
    for r in rows:
        text = r.get("text", "") or ""
        out.append({
            "phelps": r["phelps_code"],
            "incipit": normalize_first_words(text, 12),
            "tlen": int(r.get("tlen", 0) or 0),
            "head": text[:300],
        })
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lang", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    bpapp = fetch_book_prayers(args.lang, "bpapp")
    bpnet = fetch_book_prayers(args.lang, "bpnet")
    print(f"[{args.lang}] bpapp={len(bpapp)} bpnet={len(bpnet)}", file=sys.stderr)

    if not bpapp or not bpnet:
        print(f"[{args.lang}] need both books", file=sys.stderr)
        return

    # Index bpnet by first 5 distinctive content words
    stop = STOPWORDS.get(args.lang, STOPWORDS["en"])
    def keywords(incipit):
        words = [w for w in incipit.split() if w not in stop and len(w) > 2][:6]
        return tuple(sorted(words))

    # Build inverted index for bpnet
    by_kw = {}
    for n in bpnet:
        kws = keywords(n["incipit"])
        for k in kws:
            by_kw.setdefault(k, []).append(n)

    matches = []  # (bpapp_phelps, bpnet_phelps, score, reason)
    for a in bpapp:
        if not a["incipit"]:
            continue
        a_kws = keywords(a["incipit"])
        if len(a_kws) < 3:
            continue
        # Find candidates that share ≥3 keywords
        cand_count = {}
        for k in a_kws:
            for n in by_kw.get(k, []):
                cand_count[id(n)] = cand_count.get(id(n), 0) + 1
        # Score candidates
        best = None
        for nid, count in cand_count.items():
            if count < 3:
                continue
            n = next(x for x in bpnet if id(x) == nid)
            if n["phelps"] == a["phelps"]:
                continue  # same code already; nothing to reconcile
            # proximity score: count words from a's first-12 in order in n's head
            a_words = [w for w in a["incipit"].split() if len(w) > 2][:10]
            head_lower = n["head"].lower()
            pos = 0; hit = 0; last = -1
            for w in a_words:
                idx = head_lower.find(w, pos)
                if idx >= 0:
                    hit += 1; last = idx; pos = idx + len(w)
            score = hit / max(1, len(a_words))
            if hit == len(a_words) and last < 250:
                score += 0.3
            # Length disambig
            if a["tlen"] and n["tlen"]:
                ratio = max(a["tlen"], n["tlen"]) / max(min(a["tlen"], n["tlen"]), 1)
                if ratio > 3:
                    score -= 0.25
            if score >= 0.7 and (best is None or score > best[0]):
                best = (score, n)
        if best:
            score, n = best
            matches.append((a["phelps"], n["phelps"], score, a["incipit"][:60]))

    # Filter and write SQL
    out = open(args.out, "w")
    out.write(f"-- cross-source phelps reconciliation for {args.lang}\n")
    out.write(f"-- bpapp prayers: {len(bpapp)}, bpnet prayers: {len(bpnet)}\n")
    out.write(f"-- {len(matches)} candidate reconciliations (bpapp_phelps differs from matching bpnet text)\n")
    out.write("SET FOREIGN_KEY_CHECKS=0;\n\n")

    n_tmp_resolves = 0
    n_renames = 0
    for a_ph, n_ph, score, incipit in matches:
        confidence = "HIGH" if score >= 1.0 else "MED"
        if a_ph.startswith("TMP") and not n_ph.startswith("TMP"):
            # Easy case: TMP → real phelps
            out.write(f"-- {confidence} score={score:.2f}: {a_ph} → {n_ph} ({incipit})\n")
            out.write(f"-- TMP-resolve: bpapp's TMP matches bpnet's canonical phelps\n")
            out.write(f"UPDATE writings SET phelps='{n_ph}' WHERE phelps='{a_ph}' AND language='{args.lang}';\n")
            out.write(f"UPDATE prayer_book_structure SET phelps_code='{n_ph}', notes='cross-source-tmp-resolve; was {a_ph}' WHERE source_language='{args.lang}:bpapp' AND phelps_code='{a_ph}';\n\n")
            n_tmp_resolves += 1
        else:
            # Both real but different — manual review only
            out.write(f"-- {confidence} score={score:.2f}: bpapp {a_ph} ≠ bpnet {n_ph} for {incipit}\n")
            out.write(f"-- (review needed; both look real — pick canonical or treat as cross-source variants)\n\n")
            n_renames += 1
    out.write(f"\n-- Summary: {n_tmp_resolves} auto TMP-resolves, {n_renames} differing-real-phelps for review\n")
    out.write("SET FOREIGN_KEY_CHECKS=1;\n")
    out.close()
    print(f"[{args.lang}] {n_tmp_resolves} TMP-resolves, {n_renames} both-real disagreements")


if __name__ == "__main__":
    main()
