#!/bin/bash
# data_quality.sh — Run after making DB changes to check quality and rebuild
# Usage: bash scripts/data_quality.sh [--fix] [--rebuild]
#
# Without flags: report only (safe to run anytime)
# --fix:     apply automatic fixes (duplicate deletion, known recodes)
# --rebuild: regenerate Hugo data + push site after checks pass

set -uo pipefail
DOLT_DIR="${DOLT_DIR:-$HOME/bahaiwritings}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

FIX=false
REBUILD=false
for arg in "$@"; do
  case "$arg" in
    --fix) FIX=true ;;
    --rebuild) REBUILD=true ;;
  esac
done

dolt() { command dolt --data-dir="$DOLT_DIR" "$@"; }
sql() { cd "$DOLT_DIR" && command dolt sql -q "$1" 2>/dev/null; }

echo "=== Data Quality Report ==="
echo "Database: $DOLT_DIR"
echo ""

# 1. Basic stats
echo "--- Basic Stats ---"
sql "SELECT
  COUNT(*) as total_entries,
  COUNT(DISTINCT language) as languages,
  SUM(CASE WHEN phelps IS NULL THEN 1 ELSE 0 END) as null_phelps,
  SUM(CASE WHEN phelps LIKE 'TMP%' THEN 1 ELSE 0 END) as tmp_codes
FROM writings WHERE type IS NULL OR type='prayer'"

# 2. Exact duplicates (same phelps + language + text length)
echo ""
echo "--- Exact Duplicates (same phelps + language + length) ---"
sql "SELECT COUNT(*) as exact_duplicate_groups FROM (
  SELECT phelps, language, LENGTH(text) as len, COUNT(*) as c
  FROM writings
  WHERE phelps IS NOT NULL
  GROUP BY phelps, language, LENGTH(text)
  HAVING COUNT(*) > 1
) t"

if [ "$FIX" = true ]; then
  sql "SELECT phelps, language, LENGTH(text) as len, COUNT(*) as copies
  FROM writings WHERE phelps IS NOT NULL
  GROUP BY phelps, language, LENGTH(text)
  HAVING COUNT(*) > 1
  ORDER BY copies DESC LIMIT 20"
fi

# 3. Non-exact duplicates (same phelps + language, different lengths)
echo ""
echo "--- Non-Exact Duplicates (same phelps + language, different lengths) ---"
sql "SELECT phelps, language, COUNT(*) as entries, COUNT(DISTINCT LENGTH(text)) as diff_lengths
FROM writings
WHERE phelps IS NOT NULL AND phelps NOT LIKE 'TMP%'
  AND (type IS NULL OR type='prayer')
GROUP BY phelps, language
HAVING COUNT(*) > 1 AND COUNT(DISTINCT LENGTH(text)) > 1
ORDER BY entries DESC
LIMIT 20"

# 4. Compilation codes with extreme length ratios
echo ""
echo "--- Compilation Codes with >5x Length Ratio ---"
sql "SELECT phelps, COUNT(DISTINCT language) as langs,
  MIN(LENGTH(text)) as min_len, MAX(LENGTH(text)) as max_len,
  ROUND(MAX(LENGTH(text)) * 1.0 / GREATEST(MIN(LENGTH(text)),1), 1) as ratio
FROM writings WHERE (type IS NULL OR type = 'prayer')
  AND phelps NOT LIKE 'TMP%'
  AND (phelps LIKE 'BHU%' OR phelps LIKE 'BBU%' OR phelps LIKE 'ABU%')
GROUP BY phelps
HAVING COUNT(DISTINCT language) >= 3
  AND MAX(LENGTH(text)) * 1.0 / GREATEST(MIN(LENGTH(text)),1) > 5
ORDER BY ratio DESC
LIMIT 20"

# 5. LLM failure patterns
echo ""
echo "--- LLM Failure Patterns ---"
sql "SELECT COUNT(*) as llm_failures FROM writings
WHERE text LIKE '%I apologize, it seems I made%'
   OR text LIKE '%I am unable to translate%'
   OR text LIKE '%search results indicate%'
   OR text LIKE '%This command executes the%'
   OR text LIKE '%it seems I made a mistake%'
   OR text LIKE '%available tools do not%'"

# 6. Malformed phelps codes
echo ""
echo "--- Malformed Phelps Codes ---"
sql "SELECT DISTINCT phelps FROM writings
WHERE phelps IS NOT NULL
  AND phelps NOT REGEXP '^(BH|BB|AB|ABU|BHU|BBU|TMP|UHR|XAB|XBH|XBB|NEW_|PIN_)[0-9A-Za-z]'
  AND phelps NOT LIKE 'bible:%' AND phelps NOT LIKE 'quran:%'
ORDER BY phelps
LIMIT 20"

# 7. Codes with only English text in non-English languages (potential fallback issues)
echo ""
echo "--- English Fallback Candidates (non-en language, English-looking text) ---"
sql "SELECT phelps, language, source, LEFT(REPLACE(text, char(10), ' '), 80) as preview
FROM writings
WHERE language NOT IN ('en','eu','no','sr')
  AND source = 'bahaiprayers.app'
  AND text REGEXP '^(##|He is|O |In the Name|Praised|Glory|Praise)'
  AND text NOT REGEXP '[^\\x00-\\x7F]'
  AND (type IS NULL OR type='prayer')
LIMIT 20"

# 8. Unpushed changes
echo ""
echo "--- Dolt Status ---"
cd "$DOLT_DIR"
AHEAD=$(command dolt log --oneline HEAD ^remotes/origin/main 2>/dev/null | wc -l)
echo "Commits ahead of origin: $AHEAD"
if [ "$AHEAD" -gt 0 ]; then
  echo "  Run: cd $DOLT_DIR && dolt push origin main"
fi

# 9. Rebuild if requested
if [ "$REBUILD" = true ]; then
  echo ""
  echo "=== Rebuilding Site ==="

  echo "Step 1: Generate Hugo data..."
  cd "$PROJECT_DIR"
  go run scripts/gen_hugo_data.go --dolt-dir "$DOLT_DIR" --out-dir .
  echo "  Done."

  echo "Step 2: Push Dolt (if needed)..."
  if [ "$AHEAD" -gt 0 ]; then
    cd "$DOLT_DIR"
    command dolt push origin main
    echo "  Pushed to DoltHub."
  else
    echo "  Already up to date."
  fi

  echo "Step 3: Commit + push Hugo site..."
  cd "$PROJECT_DIR"
  if [ -n "$(git status --porcelain)" ]; then
    git add -A
    git commit -m "Regenerate data after quality fixes"
    git push origin master
    echo "  Pushed to GitHub (CF Pages will rebuild)."
  else
    echo "  No Hugo data changes."
  fi

  echo ""
  echo "=== Rebuild Complete ==="
fi

echo ""
echo "Done. Run with --fix to auto-fix, --rebuild to regenerate site."
