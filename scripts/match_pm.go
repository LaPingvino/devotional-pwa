// match_pm.go — Match Prayers & Meditations entries (BH09700001-BH09700184) to real Phelps codes.
//
// Usage:
//   cd ~/bahaiwritings
//   go run ~/prayermatching/scripts/match_pm.go
//
// Outputs /tmp/pm_remap.sql and applies it.
package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var htmlRe = regexp.MustCompile(`<[^>]*>`)
var multiSpace = regexp.MustCompile(`\s+`)

func stripHTML(s string) string {
	s = htmlRe.ReplaceAllString(s, " ")
	s = multiSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func normalize(s string) string {
	s = stripHTML(s)
	s = strings.ToLower(s)
	// Remove punctuation
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(multiSpace.ReplaceAllString(b.String(), " "))
}

func words(s string) map[string]bool {
	m := make(map[string]bool)
	for _, w := range strings.Fields(s) {
		if len(w) > 2 { // skip very short words
			m[w] = true
		}
	}
	return m
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func prefixMatch(a, b string, n int) float64 {
	if len(a) > n {
		a = a[:n]
	}
	if len(b) > n {
		b = b[:n]
	}
	if a == b {
		return 1.0
	}
	// Character-level similarity for prefix
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	common := 0
	for i := 0; i < minLen; i++ {
		if a[i] == b[i] {
			common++
		} else {
			break
		}
	}
	return float64(common) / float64(n)
}

type candidate struct {
	phelps string
	score  float64
	method string
}

func doltQuery(query string) [][]string {
	cmd := exec.Command("dolt", "sql", "-q", query, "--result-format", "csv")
	cmd.Dir = os.Getenv("HOME") + "/bahaiwritings"
	out, err := cmd.Output()
	if err != nil {
		log.Printf("Query failed: %s\nError: %v", query, err)
		return nil
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	records, err := r.ReadAll()
	if err != nil {
		log.Printf("CSV parse failed: %v", err)
		return nil
	}
	if len(records) <= 1 {
		return nil
	}
	return records[1:] // skip header
}

func main() {
	log.SetFlags(0)

	// 1. Load PM entries
	log.Println("Loading PM entries...")
	pmRows := doltQuery(`SELECT phelps, REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(text, '<p>', ''), '</p>', ''), '<br>', ' '), '<br/>', ' '), '&#39;', '''') as clean FROM writings WHERE type='pm' AND language='en' ORDER BY phelps`)
	if pmRows == nil {
		log.Fatal("No PM entries found")
	}
	log.Printf("Loaded %d PM entries", len(pmRows))

	type entry struct {
		phelps string
		text   string
		norm   string
		words  map[string]bool
	}

	pmEntries := make([]entry, len(pmRows))
	for i, r := range pmRows {
		txt := stripHTML(r[1])
		n := normalize(txt)
		pmEntries[i] = entry{r[0], txt, n, words(n)}
	}

	// 2. Load inventory entries with PMP references (the strongest signal)
	log.Println("Loading PMP inventory entries...")
	pmpRows := doltQuery("SELECT PIN, `First line (translated)` FROM inventory WHERE prefix='BH' AND Publications LIKE '%PMP%' ORDER BY PIN")
	log.Printf("Loaded %d PMP inventory entries", len(pmpRows))

	pmpEntries := make([]entry, 0, len(pmpRows))
	for _, r := range pmpRows {
		if r[1] == "" {
			continue
		}
		txt := stripHTML(r[1])
		n := normalize(txt)
		pmpEntries = append(pmpEntries, entry{r[0], txt, n, words(n)})
	}

	// 3. Load inventory fulltext (English, BH)
	log.Println("Loading inventory fulltext...")
	ftRows := doltQuery("SELECT phelps, text FROM inventory_fulltext WHERE language='en' AND phelps LIKE 'BH%'")
	log.Printf("Loaded %d fulltext entries", len(ftRows))

	ftEntries := make([]entry, 0, len(ftRows))
	for _, r := range ftRows {
		txt := stripHTML(r[1])
		n := normalize(txt)
		ftEntries = append(ftEntries, entry{r[0], txt, n, words(n)})
	}

	// 4. Load existing English BH prayers
	log.Println("Loading existing English prayers...")
	prRows := doltQuery("SELECT phelps, REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(text, '<p>', ''), '</p>', ''), '<br>', ' '), '<br/>', ' '), '&#39;', '''') as clean FROM writings WHERE language='en' AND (type IS NULL OR type='prayer') AND phelps LIKE 'BH%' AND phelps NOT LIKE 'BH097%'")
	log.Printf("Loaded %d existing prayer entries", len(prRows))

	prEntries := make([]entry, 0, len(prRows))
	for _, r := range prRows {
		txt := stripHTML(r[1])
		n := normalize(txt)
		prEntries = append(prEntries, entry{r[0], txt, n, words(n)})
	}

	// 5. Manual overrides for cases where inventory_fulltext has wrong text
	// or where multiple PM sections map to the same tablet.
	// These were verified against the inventory Publications field (PMP#NNN).
	manualOverrides := map[string]string{
		"BH09700002": "BH05823", // PMP#002; ft had BH04053's text
		"BH09700053": "BH07847", // PMP#053; ft matched BH05894 (PMP#089) incorrectly
		"BH09700058": "BH00769", // PMP#057+058 same tablet; 057 already claimed BH00769
		"BH09700089": "BH05894", // PMP#089; was blocked by PM053's incorrect claim
		"BH09700107": "BH07297", // PMP#107; first line starts differently in inventory
		"BH09700135": "BH04053", // PMP#135; ft had PM002's text
		"BH09700150": "BH02848", // PMP#150; ft for BH08827 had this text
	}

	// 5b. Match each PM entry
	type match struct {
		oldPhelps string
		newPhelps string
		score     float64
		method    string
	}

	var matches []match
	var unmatched []string

	for _, pm := range pmEntries {
		// Check manual overrides first
		if code, ok := manualOverrides[pm.phelps]; ok {
			matches = append(matches, match{pm.phelps, code, 1.0, "manual-override"})
			continue
		}

		var best candidate

		// Strategy 1: Match against PMP inventory first lines (prefix match)
		for _, inv := range pmpEntries {
			ps := prefixMatch(pm.norm, inv.norm, 150)
			if ps > best.score {
				best = candidate{inv.phelps, ps, "pmp-prefix"}
			}
			js := jaccard(pm.words, inv.words)
			if js > best.score {
				best = candidate{inv.phelps, js, "pmp-jaccard"}
			}
		}

		// Strategy 2: Match against inventory fulltext
		for _, ft := range ftEntries {
			ps := prefixMatch(pm.norm, ft.norm, 150)
			if ps > best.score {
				best = candidate{ft.phelps, ps, "ft-prefix"}
			}
			js := jaccard(pm.words, ft.words)
			if js > best.score {
				best = candidate{ft.phelps, js, "ft-jaccard"}
			}
		}

		// Strategy 3: Match against existing English prayers
		for _, pr := range prEntries {
			ps := prefixMatch(pm.norm, pr.norm, 150)
			if ps > best.score {
				best = candidate{pr.phelps, ps, "prayer-prefix"}
			}
			js := jaccard(pm.words, pr.words)
			if js > best.score {
				best = candidate{pr.phelps, js, "prayer-jaccard"}
			}
		}

		threshold := 0.45
		if best.score >= threshold && best.phelps != "" {
			matches = append(matches, match{pm.phelps, best.phelps, best.score, best.method})
		} else {
			unmatched = append(unmatched, pm.phelps)
			if best.phelps != "" {
				log.Printf("LOW SCORE: %s -> %s (%.3f %s) [%.60s]",
					pm.phelps, best.phelps, best.score, best.method, pm.text)
			} else {
				log.Printf("NO MATCH: %s [%.60s]", pm.phelps, pm.text)
			}
		}
	}

	// Sort matches by old phelps code
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].oldPhelps < matches[j].oldPhelps
	})

	// 6. Generate SQL
	sqlFile := "/tmp/claude/pm_remap.sql"
	f, err := os.Create(sqlFile)
	if err != nil {
		log.Fatalf("Cannot create %s: %v", sqlFile, err)
	}

	fmt.Fprintln(f, "SET FOREIGN_KEY_CHECKS=0;")
	for _, m := range matches {
		// Update ALL languages for this PM entry, not just English
		// The PM number (e.g. BH09700042) is shared across languages
		fmt.Fprintf(f, "-- %s -> %s (%.3f %s)\n", m.oldPhelps, m.newPhelps, m.score, m.method)
		fmt.Fprintf(f, "UPDATE writings SET phelps='%s' WHERE phelps='%s';\n", m.newPhelps, m.oldPhelps)
	}
	fmt.Fprintln(f, "SET FOREIGN_KEY_CHECKS=1;")
	f.Close()

	log.Printf("\n=== RESULTS ===")
	log.Printf("Matched: %d / %d", len(matches), len(pmEntries))
	log.Printf("Unmatched: %d", len(unmatched))
	log.Printf("SQL written to: %s", sqlFile)

	if len(unmatched) > 0 {
		log.Printf("\nUnmatched PM entries:")
		for _, u := range unmatched {
			log.Printf("  %s", u)
		}
	}

	// Print match summary
	log.Printf("\nMatches:")
	for _, m := range matches {
		log.Printf("  %s -> %s (%.3f %s)", m.oldPhelps, m.newPhelps, m.score, m.method)
	}
}
