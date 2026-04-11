#!/bin/bash
# doltlite_sync.sh — Create/refresh a doltlite working copy from the Dolt DB
# Usage: bash scripts/doltlite_sync.sh [output_path]
#
# Creates a SQLite-compatible working database for matching/analysis
# without needing a Dolt server or risking manifest locks.

set -euo pipefail
DOLT_DIR="${DOLT_DIR:-$HOME/bahaiwritings}"
OUT="${1:-/tmp/claude/prayers.doltlite}"

echo "Exporting from Dolt ($DOLT_DIR) to doltlite ($OUT)..."

# Export prayers
echo "→ Exporting prayers..."
cd "$DOLT_DIR"
dolt sql -r csv -q "SELECT phelps, language, version, source, source_id, name, LENGTH(text) as text_len, LEFT(text, 500) as text_head, text FROM writings WHERE (type IS NULL OR type='prayer')" > /tmp/claude/_prayers.csv
echo "  $(wc -l < /tmp/claude/_prayers.csv) rows"

# Export inventory
echo "→ Exporting inventory..."
dolt sql -r csv -q "SELECT PIN, \`First line (translated)\` as en_first_line FROM inventory WHERE \`First line (translated)\` IS NOT NULL AND \`First line (translated)\` <> ''" > /tmp/claude/_inventory.csv
echo "  $(wc -l < /tmp/claude/_inventory.csv) rows"

# Remove old db
rm -f "$OUT"

# Import into doltlite
echo "→ Importing into doltlite..."
doltlite "$OUT" ".import /tmp/claude/_prayers.csv prayers"
doltlite "$OUT" ".import /tmp/claude/_inventory.csv inventory"

# Verify
echo "→ Verifying..."
P=$(doltlite "$OUT" "SELECT COUNT(*) FROM prayers")
I=$(doltlite "$OUT" "SELECT COUNT(*) FROM inventory")
echo "  Prayers: $P, Inventory: $I"

# Cleanup
rm -f /tmp/claude/_prayers.csv /tmp/claude/_inventory.csv

echo "Done! Working DB at: $OUT"
echo "Query with: doltlite $OUT \"SELECT ...\""
