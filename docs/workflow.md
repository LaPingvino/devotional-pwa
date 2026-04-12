# Workflow Reference

## Matching prayers

```bash
cd ~/prayermatching/scripts
go run match.go --lang XX                       # auto-match unresolved
go run match.go --lang XX --verify --reverify   # quality check
```

## Applying SQL changes

```bash
cd ~/bahaiwritings
# IMPORTANT: use grep to pipe, NOT dolt sql < file.sql (loses SET context)
grep "^SET\|^UPDATE\|^DELETE\|^INSERT" file.sql | dolt sql

# Or for clean SQL files without stderr:
dolt sql < clean_file.sql
```

## Committing to Dolt

```bash
cd ~/bahaiwritings
dolt add .               # or: dolt add writings
dolt commit -m "..."
dolt push origin main
```

**Known issue:** `dolt add writings` sometimes fails with "database is read only" when `dolt add .` works. See dolthub/dolt#10852.

## Regenerating the website

```bash
cd ~/prayermatching/devotional-pwa
go run scripts/gen_hugo_data.go --dolt-dir ~/bahaiwritings --out-dir .
git add assets/ data/ static/
git commit -m "Regenerate data"
git push origin master    # CF Pages auto-builds
```

## TMP code allocation

```sql
SELECT MAX(CAST(SUBSTRING(phelps, 4) AS UNSIGNED)) FROM writings WHERE phelps LIKE 'TMP%';
-- Next TMP = that number + 1, zero-padded to 5 digits
SET FOREIGN_KEY_CHECKS=0;
UPDATE writings SET phelps='TMP00XXX' WHERE version='...';
SET FOREIGN_KEY_CHECKS=1;
```

## Paragraph alignment (writings)

```bash
# Review flagged paragraphs:
go run scripts/review_align.go --lang XX --type iqan --threshold 2.0 --flag-only

# Semi-interactive alignment:
go run scripts/manual_align.go --lang XX --type iqan --part 2 --reset
go run scripts/manual_align.go --lang XX --type iqan --part 2 --decide "P,M2,P,P,..."
go run scripts/manual_align.go --lang XX --type iqan --part 2 --apply 2>/dev/null | dolt sql
```

Decisions: P=pass, M2=merge 2, M3=merge 3, E=skip English, K=skip target.

## Fingerprint benchmarking

```bash
cd ~/bahaiwritings
go run ~/prayermatching/scripts/fp_bench.go --quick          # fast test
go run ~/prayermatching/scripts/fp_bench.go --quick --ablation  # feature importance
```

## Dolt SQL gotchas

- Use `<>` not `!=` (zsh escapes `!`)
- `SET FOREIGN_KEY_CHECKS=0;` before UPDATE/DELETE on phelps; reset after
- PK is `version` (UUID), not source_id
- Manifest lock: commit frequently, avoid concurrent dolt processes
- If "database is read only": check for stale `.dolt/sql-server.info`, kill orphan processes, or re-clone

## Importing from bahai.org

All JS-rendered pages have XHTML/PDF/DOCX versions:
```
https://www.bahai.org/library/.../book-name/book-name.xhtml?TOKEN
```
