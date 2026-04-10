// backfill_fulltext.go — Populate inventory_fulltext from existing English writings entries.
//
// For each phelps code that has an English entry in writings but no entry in inventory_fulltext,
// split the English text into 850-char parts and insert them.
//
// Usage: go run backfill_fulltext.go [--dry-run] [--limit N]
package main

import (
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const backfillDoltDir = "/home/joop/bahaiwritings"
const backfillPartSize = 850

func bfQuery(q string) []map[string]string {
	cmd := exec.Command("dolt", "sql", "-q", q, "--result-format", "csv")
	cmd.Dir = backfillDoltDir
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dolt error] %v\n", err)
		return nil
	}
	r := csv.NewReader(bytes.NewReader(out))
	r.LazyQuotes = true
	rows, _ := r.ReadAll()
	if len(rows) < 2 {
		return nil
	}
	headers := rows[0]
	var result []map[string]string
	for _, row := range rows[1:] {
		m := map[string]string{}
		for i, h := range headers {
			if i < len(row) {
				m[h] = row[i]
			}
		}
		result = append(result, m)
	}
	return result
}

func bfExec(sql string) {
	cmd := exec.Command("dolt", "sql", "-q", sql)
	cmd.Dir = backfillDoltDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dolt exec error] %v: %s\n", err, string(out))
	}
}

func bfSqlEsc(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(s)
}

func bfSplitParts(text string, size int) []string {
	// Strip leading markdown header lines (## ...) which are not the prayer text
	lines := strings.Split(text, "\n")
	var filtered []string
	inHeader := true
	for _, line := range lines {
		if inHeader && (strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "") {
			continue
		}
		inHeader = false
		filtered = append(filtered, line)
	}
	text = strings.TrimSpace(strings.Join(filtered, "\n"))

	var parts []string
	for len(text) > size {
		cut := size
		// Try to split at a space boundary
		for cut > size-100 && cut > 0 && text[cut] != ' ' {
			cut--
		}
		if cut == 0 {
			cut = size
		}
		parts = append(parts, text[:cut])
		text = strings.TrimSpace(text[cut:])
	}
	if len(text) > 0 {
		parts = append(parts, text)
	}
	return parts
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Print SQL but don't execute")
	limit := flag.Int("limit", 1000, "Max number of phelps codes to process")
	flag.Parse()

	// Find phelps codes with English writings but no inventory_fulltext
	rows := bfQuery(fmt.Sprintf(`
SELECT w.phelps, w.text
FROM writings w
LEFT JOIN inventory_fulltext f ON w.phelps=f.phelps AND f.language='en'
WHERE w.language='en'
  AND w.phelps NOT LIKE 'TMP%%'
  AND w.phelps NOT LIKE 'XAB%%'
  AND w.phelps NOT LIKE 'XBH%%'
  AND w.phelps NOT LIKE 'XBB%%'
  AND w.source <> 'llm-translation'
  AND w.phelps NOT REGEXP '[A-Z]{2,3}[0-9]{4,5}[A-Z0-9]{2,4}$'
  AND f.phelps IS NULL
GROUP BY w.phelps, w.text
ORDER BY w.phelps
LIMIT %d`, *limit))

	fmt.Fprintf(os.Stderr, "Found %d phelps codes to backfill\n", len(rows))

	total := 0
	for _, row := range rows {
		phelps := row["phelps"]
		text := row["text"]
		parts := bfSplitParts(text, backfillPartSize)
		if len(parts) == 0 {
			continue
		}
		for i, part := range parts {
			if len(strings.TrimSpace(part)) < 20 {
				continue
			}
			sql := fmt.Sprintf(
				"INSERT INTO inventory_fulltext (phelps, language, part, text, source) VALUES ('%s', 'en', %d, '%s', 'writings') ON DUPLICATE KEY UPDATE text=VALUES(text);",
				bfSqlEsc(phelps), i, bfSqlEsc(part),
			)
			if *dryRun {
				fmt.Printf("-- %s part %d (%d chars)\n", phelps, i, len(part))
			} else {
				bfExec(sql)
			}
			total++
		}
		if !*dryRun {
			fmt.Printf("  %s (%d parts)\n", phelps, len(parts))
		}
	}
	fmt.Fprintf(os.Stderr, "Done: %d parts inserted for %d codes\n", total, len(rows))
}
