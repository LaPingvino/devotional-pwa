#!/usr/bin/env python3
"""Generate safe Turkish recategorization SQL, skipping codes already in non-T&D categories."""
import csv, subprocess, io, os

TMPDIR = os.environ.get("TMPDIR", "/tmp")
dolt_dir = os.path.expanduser("~/bahaiwritings")

def dq(q):
    r = subprocess.run(["dolt", "sql", "-q", q, "--result-format", "csv"],
                       capture_output=True, text=True, cwd=dolt_dir)
    return list(csv.DictReader(io.StringIO(r.stdout)))

all_pbs = dq("SELECT category_name, phelps_code FROM prayer_book_structure WHERE source_language='tr'")
existing_codes = set()
for r in all_pbs:
    if r["category_name"] not in ("Tests and Difficulties",):
        existing_codes.add(r["phelps_code"])

with open(f"{TMPDIR}/tr_recat_final.sql") as f:
    lines = f.readlines()

sql = ["SET FOREIGN_KEY_CHECKS=0;"]
sql.append("DELETE FROM prayer_book_structure WHERE source_language='tr' AND category_name='Tests and Difficulties';")

skipped = 0
inserted = 0
for line in lines:
    line = line.strip()
    if not line.startswith("INSERT"):
        continue
    parts = line.split("'")
    code = parts[5]
    if code in existing_codes:
        skipped += 1
        continue
    safe = line.rstrip(";") + " ON DUPLICATE KEY UPDATE order_in_category=VALUES(order_in_category);"
    sql.append(safe)
    inserted += 1

sql.append("SET FOREIGN_KEY_CHECKS=1;")

out = f"{TMPDIR}/tr_recat_safe.sql"
with open(out, "w") as f:
    f.write("\n".join(sql))
print(f"Skipped {skipped} already-categorized, writing {inserted} inserts to {out}")
