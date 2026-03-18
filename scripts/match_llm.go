// match_llm.go — Verify and apply phelps code upgrades for llm-translation prayers.
//
// For each 7-char phelps code in the llm-translation source that uniquely maps
// to one full mnemonic code in another source, compare the LLM English text
// against the canonical English text using word-overlap similarity.
// Prints a report and (with --apply) writes SQL UPDATE statements.
//
// Usage:
//
//	go run scripts/match_llm.go --db ./bahaiwritings
//	go run scripts/match_llm.go --db ./bahaiwritings --apply > fix_llm.sql
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

func doltCSV(dbPath, sql string) [][]string {
	cmd := exec.Command("dolt", "sql", "-q", sql, "--result-format", "csv")
	cmd.Dir = dbPath
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dolt error: %v\n%s\n", err, out)
		os.Exit(1)
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	r.LazyQuotes = true
	rows, _ := r.ReadAll()
	if len(rows) < 2 {
		return nil
	}
	return rows[1:]
}

var wordRe = regexp.MustCompile(`[a-z]+`)

// similarity returns overlap coefficient: |intersection| / min(|A|,|B|)
func similarity(a, b string) float64 {
	wa := wordRe.FindAllString(strings.ToLower(a), -1)
	wb := wordRe.FindAllString(strings.ToLower(b), -1)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	setA := map[string]bool{}
	for _, w := range wa {
		if len(w) > 3 { // skip tiny words
			setA[w] = true
		}
	}
	setB := map[string]bool{}
	for _, w := range wb {
		if len(w) > 3 {
			setB[w] = true
		}
	}
	inter := 0
	for w := range setA {
		if setB[w] {
			inter++
		}
	}
	min := len(setA)
	if len(setB) < min {
		min = len(setB)
	}
	if min == 0 {
		return 0
	}
	return float64(inter) / float64(min)
}

func main() {
	db := flag.String("db", "", "Path to dolt repo (required)")
	apply := flag.Bool("apply", false, "Print SQL UPDATE statements for matches")
	threshold := flag.Float64("threshold", 0.25, "Min similarity to accept (0-1)")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "Usage: go run match_llm.go --db ./bahaiwritings [--apply] [--threshold 0.25]")
		os.Exit(1)
	}

	// Find all unambiguous short→full mappings
	pairs := doltCSV(*db, `
SELECT llm.phelps AS short_p, other.phelps AS full_p
FROM writings llm
JOIN writings other
  ON LEFT(llm.phelps, 7) = LEFT(other.phelps, 7)
  AND LENGTH(llm.phelps) = 7
  AND LENGTH(other.phelps) > 7
WHERE llm.source = 'llm-translation'
  AND other.source <> 'llm-translation'
GROUP BY llm.phelps, other.phelps
HAVING COUNT(*) >= 1
`)

	// Keep only codes that map to exactly one full_p
	countMap := map[string][]string{}
	for _, r := range pairs {
		if len(r) < 2 {
			continue
		}
		countMap[r[0]] = append(countMap[r[0]], r[1])
	}
	type pair struct{ short, full string }
	var uniq []pair
	for short, fulls := range countMap {
		if len(fulls) == 1 {
			uniq = append(uniq, pair{short, fulls[0]})
		}
	}
	sort.Slice(uniq, func(i, j int) bool { return uniq[i].short < uniq[j].short })

	// Fetch LLM English texts
	shortList := make([]string, len(uniq))
	for i, p := range uniq {
		shortList[i] = "'" + p.short + "'"
	}
	inClause := strings.Join(shortList, ",")

	llmTexts := doltCSV(*db, fmt.Sprintf(`
SELECT phelps, text FROM writings
WHERE source = 'llm-translation' AND phelps IN (%s)`, inClause))
	llmMap := map[string]string{}
	for _, r := range llmTexts {
		if len(r) >= 2 {
			llmMap[r[0]] = r[1]
		}
	}

	// Fetch canonical English texts for full codes
	fullList := make([]string, len(uniq))
	for i, p := range uniq {
		fullList[i] = "'" + p.full + "'"
	}
	inFull := strings.Join(fullList, ",")

	canonTexts := doltCSV(*db, fmt.Sprintf(`
SELECT phelps, text FROM writings
WHERE language = 'en' AND source <> 'llm-translation' AND phelps IN (%s)
ORDER BY FIELD(source,'bahaiprayers.net','bahaiprayers.app','bahaiprayers.org')`, inFull))
	canonMap := map[string]string{}
	for _, r := range canonTexts {
		if len(r) >= 2 {
			if _, exists := canonMap[r[0]]; !exists {
				canonMap[r[0]] = r[1]
			}
		}
	}

	// Count rows affected per short code (all sources/languages)
	affectedRows := doltCSV(*db, fmt.Sprintf(`
SELECT phelps, COUNT(*) as cnt FROM writings
WHERE phelps IN (%s)
GROUP BY phelps`, inClause))
	affectedMap := map[string]string{}
	for _, r := range affectedRows {
		if len(r) >= 2 {
			affectedMap[r[0]] = r[1]
		}
	}

	if *apply {
		fmt.Println("SET FOREIGN_KEY_CHECKS=0;")
	} else {
		fmt.Printf("%-12s %-12s %6s %6s  %s\n", "SHORT", "FULL", "SIM", "ROWS", "VERDICT")
		fmt.Println(strings.Repeat("-", 70))
	}

	matched, skipped := 0, 0
	for _, p := range uniq {
		llmText := llmMap[p.short]
		canonText := canonMap[p.full]
		sim := similarity(llmText, canonText)
		rows := affectedMap[p.short]

		verdict := "SKIP (low similarity)"
		if sim >= *threshold {
			verdict = "MATCH"
		}
		if canonText == "" {
			verdict = "SKIP (no canonical EN)"
		}

		if !*apply {
			fmt.Printf("%-12s %-12s %5.0f%% %6s  %s\n", p.short, p.full, sim*100, rows, verdict)
			if verdict == "MATCH" {
				// Show first 80 chars of each for quick review
				llmSnip := strings.ReplaceAll(llmText, "\n", " ")
				canSnip := strings.ReplaceAll(canonText, "\n", " ")
				if len(llmSnip) > 80 {
					llmSnip = llmSnip[:80]
				}
				if len(canSnip) > 80 {
					canSnip = canSnip[:80]
				}
				fmt.Printf("  LLM: %s\n", llmSnip)
				fmt.Printf("  CAN: %s\n", canSnip)
			}
		} else {
			if sim >= *threshold && canonText != "" {
				safe := strings.ReplaceAll(p.full, "'", "''")
				safeShort := strings.ReplaceAll(p.short, "'", "''")
				fmt.Printf("UPDATE writings SET phelps='%s' WHERE phelps='%s'; -- sim=%.0f%% rows=%s\n",
					safe, safeShort, sim*100, rows)
				matched++
			} else {
				skipped++
			}
		}
	}

	if *apply {
		fmt.Println("SET FOREIGN_KEY_CHECKS=1;")
		fmt.Fprintf(os.Stderr, "SQL generated: %d updates, %d skipped (below threshold or no canonical EN)\n", matched, skipped)
	}
}
