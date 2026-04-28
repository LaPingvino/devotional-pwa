#!/usr/bin/env python3
"""tmp_cross_source.py — Find TMP↔TMP pairs across bpapp/bpnet for the
same language (same prayer text, both coded TMP). Merge them so the
two-codes-for-one-prayer issue collapses (loser→winner, bpnet wins).

Reuses cross_source_match's detect logic (proximity-based) but keeps
only TMP↔TMP pairs.
"""
import argparse, sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from cross_source_match import fetch_book_prayers
from match_writings_incipit import STOPWORDS


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lang", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    bpapp = fetch_book_prayers(args.lang, "bpapp")
    bpnet = fetch_book_prayers(args.lang, "bpnet")
    if not bpapp or not bpnet:
        print(f"[{args.lang}] need both books", file=sys.stderr)
        return

    stop = STOPWORDS.get(args.lang, STOPWORDS["en"])

    def keywords(incipit):
        words = [w for w in incipit.split() if w not in stop and len(w) > 2][:6]
        return tuple(sorted(words))

    by_kw = {}
    for n in bpnet:
        if not n["phelps"].startswith("TMP"):
            continue
        for k in keywords(n["incipit"]):
            by_kw.setdefault(k, []).append(n)

    pairs = []
    for a in bpapp:
        if not a["phelps"].startswith("TMP") or not a["incipit"]:
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
            if count < 4:
                continue
            n = next(x for x in bpnet if id(x) == nid and x["phelps"].startswith("TMP"))
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
                if ratio > 1.8:
                    score -= 0.4
            if score >= 0.85 and (best is None or score > best[0]):
                best = (score, n)
        if best:
            pairs.append((a["phelps"], best[1]["phelps"], best[0], a["incipit"][:60]))

    out = open(args.out, "w")
    out.write(f"-- TMP↔TMP cross-source merges for {args.lang} (bpnet wins)\n")
    out.write("SET FOREIGN_KEY_CHECKS=0;\n\n")
    seen = set()
    n = 0
    # Pre-fetch existing PBS keys to handle conflicts
    from match_writings_incipit import dolt_query
    pbs_rows = dolt_query(
        f"SELECT source_language, category_name, phelps_code FROM prayer_book_structure "
        f"WHERE source_language IN ('{args.lang}:bpapp','{args.lang}:bpnet')"
    )
    keys = {(r["source_language"], r["category_name"], r["phelps_code"]) for r in pbs_rows}

    for a_ph, n_ph, score, incipit in pairs:
        if a_ph == n_ph:
            continue
        key = tuple(sorted([a_ph, n_ph]))
        if key in seen:
            continue
        seen.add(key)
        winner, loser = n_ph, a_ph  # bpnet wins
        out.write(f"-- {loser} -> {winner} (score={score:.2f}): {incipit}\n")
        out.write(f"UPDATE writings SET phelps='{winner}' WHERE phelps='{loser}' AND language='{args.lang}';\n")
        for sl in (f"{args.lang}:bpapp", f"{args.lang}:bpnet"):
            losers_in_book = [k for k in keys if k[0] == sl and k[2] == loser]
            for _, cat, _ in losers_in_book:
                if (sl, cat, winner) in keys:
                    out.write(f"DELETE FROM prayer_book_structure WHERE source_language='{sl}' AND category_name='" + cat.replace("'","''") + f"' AND phelps_code='{loser}';\n")
                else:
                    out.write(f"UPDATE prayer_book_structure SET phelps_code='{winner}', notes='tmp-cross-merge; was {loser}' WHERE source_language='{sl}' AND category_name='" + cat.replace("'","''") + f"' AND phelps_code='{loser}';\n")
                    keys.add((sl, cat, winner))
                keys.discard((sl, cat, loser))
        out.write("\n")
        n += 1
    out.write("SET FOREIGN_KEY_CHECKS=1;\n")
    out.close()
    print(f"[{args.lang}] {n} TMP↔TMP merges")


if __name__ == "__main__":
    main()
