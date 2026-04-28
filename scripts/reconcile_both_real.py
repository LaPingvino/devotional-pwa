#!/usr/bin/env python3
"""reconcile_both_real.py — For "both real" cross-source disagreements
(bpapp uses phelps A, bpnet uses phelps B for the same prayer text, both
non-TMP), pick a canonical winner per user's three-rule precedence:

  1. en-bpnet canonical: if exactly one of {A,B} appears in en-bpnet PBS,
     that one wins.
  2. Mnemonic preference: if exactly one has a 3-letter mnemonic suffix
     (e.g. BH00074BLE vs BH00074), that one wins (likely the precise
     excerpt code).
  3. en-bpnet usage count: whichever phelps is used more in en-bpnet PBS
     wins. (Final tie → skip, manual.)

Emits SQL to rename loser→winner in writings (this lang) + PBS (this lang
under both :bpapp and :bpnet). Wraps in FK_CHECKS=0.
"""
import argparse, re, sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from match_writings_incipit import (
    STOPWORDS, normalize_first_words, dolt_query,
)
from cross_source_match import fetch_book_prayers


MNEMONIC_RE = re.compile(r"^(BH|BB|AB|XAB)\d+[A-Z]{3}$")


def sql_str(s):
    return "'" + (s or "").replace("'", "''") + "'"


def has_mnemonic(phelps):
    return bool(MNEMONIC_RE.match(phelps or ""))


def build_enbpnet_index():
    """Return dict phelps_code -> usage count in en-bpnet PBS."""
    rows = dolt_query(
        "SELECT phelps_code, COUNT(*) AS n FROM prayer_book_structure "
        "WHERE source_language='en:bpnet' AND phelps_code IS NOT NULL "
        "GROUP BY phelps_code"
    )
    return {r["phelps_code"]: int(r.get("n", 0) or 0) for r in rows}


def detect_pairs(lang):
    """Re-run cross-source detection. Returns list of (a_phelps, n_phelps,
    score, incipit) for matched same-prayer-different-code pairs."""
    bpapp = fetch_book_prayers(lang, "bpapp")
    bpnet = fetch_book_prayers(lang, "bpnet")
    if not bpapp or not bpnet:
        return []
    stop = STOPWORDS.get(lang, STOPWORDS["en"])

    def keywords(incipit):
        words = [w for w in incipit.split() if w not in stop and len(w) > 2][:6]
        return tuple(sorted(words))

    by_kw = {}
    for n in bpnet:
        for k in keywords(n["incipit"]):
            by_kw.setdefault(k, []).append(n)

    pairs = []
    for a in bpapp:
        if not a["incipit"]:
            continue
        a_kws = keywords(a["incipit"])
        if len(a_kws) < 3:
            continue
        cand_count = {}
        for k in a_kws:
            for n in by_kw.get(k, []):
                cand_count[id(n)] = cand_count.get(id(n), 0) + 1
        best = None
        for nid, count in cand_count.items():
            if count < 3:
                continue
            n = next(x for x in bpnet if id(x) == nid)
            if n["phelps"] == a["phelps"]:
                continue
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
            if a["tlen"] and n["tlen"]:
                ratio = max(a["tlen"], n["tlen"]) / max(min(a["tlen"], n["tlen"]), 1)
                if ratio > 3:
                    score -= 0.25
            if score >= 0.7 and (best is None or score > best[0]):
                best = (score, n)
        if best:
            score, n = best
            pairs.append((a["phelps"], n["phelps"], score, a["incipit"][:60]))
    return pairs


def pick_winner(a, b, en_count):
    """Apply user's three-rule precedence. Returns (winner, loser, reason)
    or (None, None, reason) if undecided."""
    a_in_en = a in en_count
    b_in_en = b in en_count
    if a_in_en and not b_in_en:
        return a, b, "en-bpnet-canonical"
    if b_in_en and not a_in_en:
        return b, a, "en-bpnet-canonical"
    a_mn = has_mnemonic(a)
    b_mn = has_mnemonic(b)
    if a_mn and not b_mn:
        return a, b, "mnemonic-preference"
    if b_mn and not a_mn:
        return b, a, "mnemonic-preference"
    a_n = en_count.get(a, 0)
    b_n = en_count.get(b, 0)
    if a_n > b_n:
        return a, b, f"en-bpnet-count {a_n}>{b_n}"
    if b_n > a_n:
        return b, a, f"en-bpnet-count {b_n}>{a_n}"
    return None, None, "tied"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lang", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    en_count = build_enbpnet_index()
    print(f"[en-bpnet index] {len(en_count)} distinct phelps", file=sys.stderr)

    # Pre-fetch existing PBS keys for this lang (both books) to detect PK collisions
    pbs_rows = dolt_query(
        f"SELECT source_language, category_name, phelps_code FROM prayer_book_structure "
        f"WHERE source_language IN ('{args.lang}:bpapp','{args.lang}:bpnet')"
    )
    existing_keys = {(r["source_language"], r["category_name"], r["phelps_code"]) for r in pbs_rows}

    pairs = detect_pairs(args.lang)
    print(f"[{args.lang}] {len(pairs)} cross-source pairs", file=sys.stderr)

    out = open(args.out, "w")
    out.write(f"-- both-real cross-source reconciliation for {args.lang}\n")
    out.write("SET FOREIGN_KEY_CHECKS=0;\n\n")

    n_resolved = n_tied = n_skip_tmp = 0
    seen = set()
    for a_ph, n_ph, score, incipit in pairs:
        if a_ph.startswith("TMP") or n_ph.startswith("TMP"):
            n_skip_tmp += 1
            continue
        key = tuple(sorted([a_ph, n_ph]))
        if key in seen:
            continue
        seen.add(key)
        winner, loser, reason = pick_winner(a_ph, n_ph, en_count)
        if winner is None:
            out.write(f"-- TIED {a_ph} vs {n_ph} (en-counts {en_count.get(a_ph,0)}/{en_count.get(n_ph,0)}): {incipit}\n\n")
            n_tied += 1
            continue
        out.write(f"-- {loser} -> {winner} ({reason}) score={score:.2f}: {incipit}\n")
        out.write(f"UPDATE writings SET phelps='{winner}' WHERE phelps='{loser}' AND language='{args.lang}';\n")
        # PK-conflict-safe PBS rewrite: DELETE loser rows whose category already
        # holds the winner; UPDATE the rest.
        for sl in (f"{args.lang}:bpapp", f"{args.lang}:bpnet"):
            # Find loser rows in this book
            loser_rows = [k for k in existing_keys if k[0] == sl and k[2] == loser]
            for _, cat, _ in loser_rows:
                if (sl, cat, winner) in existing_keys:
                    out.write(f"DELETE FROM prayer_book_structure WHERE source_language='{sl}' AND category_name=" + sql_str(cat) + f" AND phelps_code='{loser}';\n")
                else:
                    out.write(f"UPDATE prayer_book_structure SET phelps_code='{winner}', notes=CONCAT(COALESCE(notes,''),'; reconcile-both-real was {loser}') WHERE source_language='{sl}' AND category_name=" + sql_str(cat) + f" AND phelps_code='{loser}';\n")
                    existing_keys.add((sl, cat, winner))
                existing_keys.discard((sl, cat, loser))
        out.write("\n")
        n_resolved += 1

    out.write("SET FOREIGN_KEY_CHECKS=1;\n")
    out.close()
    print(f"[{args.lang}] resolved={n_resolved} tied={n_tied} skip-tmp={n_skip_tmp}")


if __name__ == "__main__":
    main()
