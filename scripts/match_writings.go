// match_writings.go — Match Gleanings and SAQ staging codes to real inventory PINs
//
// Usage:
//   go run match_writings.go [--dry-run] [--dolt-dir DIR]
//
// SAQ matching:
//   Chapter number from DB name (e.g. "SAQ 11:...") → inventory title "#011"
//   All 84 chapters have 1:1 mapping. Output: AB09900NNN → AB_PIN
//
// Gleanings matching:
//   Primary: GWB#NNN cross-references in inventory_translations table (165/166)
//   Fallback: text prefix comparison against inventory_fulltext + First line
//   When multiple Gleanings come from the same source tablet, a G+section suffix
//   is added (e.g. BH00001G037) to keep codes unique.
//   GWB#131 has no inventory cross-reference and remains unmatched.
//
// SQL output: /tmp/saq_remap.sql, /tmp/gleanings_remap.sql
// Without --dry-run, also applies the SQL directly to the Dolt DB.

package main

import (
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var doltDir = os.Expand("$HOME/bahaiwritings", os.Getenv)

func doltQuery(query string) []map[string]string {
	cmd := exec.Command("dolt", "sql", "-q", query, "--result-format", "csv")
	cmd.Dir = doltDir
	out, err := cmd.Output()
	if err != nil {
		ee, _ := err.(*exec.ExitError)
		if ee != nil {
			fmt.Fprintf(os.Stderr, "[dolt error] %v: %s\n", err, string(ee.Stderr))
		} else {
			fmt.Fprintf(os.Stderr, "[dolt error] %v\n", err)
		}
		return nil
	}
	r := csv.NewReader(bytes.NewReader(out))
	rows, _ := r.ReadAll()
	if len(rows) < 2 {
		return nil
	}
	header := rows[0]
	result := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		m := make(map[string]string, len(header))
		for i, h := range header {
			if i < len(row) {
				m[h] = row[i]
			}
		}
		result = append(result, m)
	}
	return result
}

func doltExec(sql string) error {
	cmd := exec.Command("dolt", "sql", "-q", sql)
	cmd.Dir = doltDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dolt exec error] %v: %s\n", err, string(out))
	}
	return err
}

func sqlEsc(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(s)
}

// stripHTML removes HTML tags and decodes common entities, then trims whitespace.
func stripHTML(s string) string {
	// Remove tags
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, " ")
	// Decode entities
	s = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&apos;", "'",
		"&nbsp;", " ",
	).Replace(s)
	// Collapse whitespace
	ws := regexp.MustCompile(`\s+`)
	s = ws.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// normalize collapses Unicode differences for comparison: lowercase, strip diacritics-ish,
// collapse whitespace, remove punctuation.
func normalize(s string) string {
	s = strings.ToLower(s)
	// Remove common punctuation that varies between editions
	s = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return ' '
		}
		return r
	}, s)
	ws := regexp.MustCompile(`\s+`)
	s = ws.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// ── SAQ matching ────────────────────────────────────────────────────────────

func matchSAQ() map[string]string {
	fmt.Println("=== Matching SAQ chapters ===")

	// Build map: chapter number → inventory PIN
	invRows := doltQuery(`SELECT PIN, Title FROM inventory WHERE Title LIKE 'Some Answered Questions%' ORDER BY PIN`)
	if invRows == nil {
		fmt.Fprintln(os.Stderr, "ERROR: no SAQ entries in inventory")
		return nil
	}

	chapterRe := regexp.MustCompile(`#(\d+)`)
	chapToPin := make(map[int]string)
	for _, row := range invRows {
		m := chapterRe.FindStringSubmatch(row["Title"])
		if m != nil {
			n, _ := strconv.Atoi(m[1])
			chapToPin[n] = row["PIN"]
		}
	}
	fmt.Printf("  Inventory: %d SAQ chapters found\n", len(chapToPin))

	// Get distinct staging codes
	dbRows := doltQuery(`SELECT DISTINCT phelps FROM writings WHERE type='saq' AND phelps LIKE 'AB099%' ORDER BY phelps`)
	if dbRows == nil {
		fmt.Fprintln(os.Stderr, "ERROR: no SAQ staging codes in writings")
		return nil
	}

	mapping := make(map[string]string)
	unmatched := 0
	for _, row := range dbRows {
		staging := row["phelps"]
		// Extract chapter number from staging code: AB09900NNN → NNN
		numStr := strings.TrimLeft(staging[len("AB099"):], "0")
		if numStr == "" {
			numStr = "0"
		}
		chapNum, _ := strconv.Atoi(numStr)

		if pin, ok := chapToPin[chapNum]; ok {
			mapping[staging] = pin
			fmt.Printf("  %s (SAQ %d) → %s\n", staging, chapNum, pin)
		} else {
			fmt.Printf("  %s (SAQ %d) → NO MATCH\n", staging, chapNum)
			unmatched++
		}
	}
	fmt.Printf("  SAQ: %d matched, %d unmatched\n", len(mapping), unmatched)
	return mapping
}

// ── Gleanings matching ──────────────────────────────────────────────────────

func matchGleanings() map[string]string {
	fmt.Println("\n=== Matching Gleanings sections ===")

	// Primary strategy: use the authoritative GWB cross-references from
	// inventory_translations. Format: "GWB#NNN" or "GWB#NNNx" where NNN is
	// the Gleaning section number (1-166).
	gwbRows := doltQuery(`SELECT PIN, display_text FROM inventory_translations WHERE display_text LIKE 'GWB#%' ORDER BY display_text`)
	gwbRe := regexp.MustCompile(`^GWB#(\d+)`)
	gwbMap := make(map[int]string) // section number → PIN
	for _, row := range gwbRows {
		m := gwbRe.FindStringSubmatch(row["display_text"])
		if m == nil {
			continue
		}
		secNum, _ := strconv.Atoi(m[1])
		// If multiple PINs for same section (some tablets appear in multiple Gleanings),
		// keep the first one — inventory_translations lists exact section→PIN.
		if _, exists := gwbMap[secNum]; !exists {
			gwbMap[secNum] = row["PIN"]
		}
	}
	fmt.Printf("  GWB cross-references loaded: %d sections\n", len(gwbMap))

	// Get distinct staging codes
	dbRows := doltQuery(`SELECT DISTINCT phelps FROM writings WHERE type='gleanings' AND phelps LIKE 'BH102%' ORDER BY phelps`)
	if dbRows == nil {
		fmt.Fprintln(os.Stderr, "ERROR: no Gleanings staging codes in writings")
		return nil
	}

	// Fallback: load inventory_fulltext and First line for text matching
	ftRows := doltQuery(`SELECT phelps, text FROM inventory_fulltext WHERE language='en' AND phelps LIKE 'BH%' AND part=0`)
	type invEntry struct {
		pin  string
		norm string
	}
	var ftEntries []invEntry
	for _, row := range ftRows {
		ftEntries = append(ftEntries, invEntry{pin: row["phelps"], norm: normalize(row["text"])})
	}
	flRows := doltQuery("SELECT PIN, `First line (translated)` as fl FROM inventory WHERE prefix='BH' AND `First line (translated)` IS NOT NULL AND LENGTH(`First line (translated)`) > 10")
	var flEntries []invEntry
	for _, row := range flRows {
		flEntries = append(flEntries, invEntry{pin: row["PIN"], norm: normalize(row["fl"])})
	}

	// Also get English text for text-based fallback
	enTextRows := doltQuery(`SELECT phelps, text FROM writings WHERE type='gleanings' AND language='en' AND phelps LIKE 'BH102%' ORDER BY phelps`)
	enText := make(map[string]string)
	for _, row := range enTextRows {
		enText[row["phelps"]] = row["text"]
	}

	// First pass: collect raw PIN mappings to detect duplicates
	type secMapping struct {
		secNum int
		pin    string
		method string
	}
	var allMappings []secMapping

	for _, row := range dbRows {
		staging := row["phelps"]
		numStr := strings.TrimLeft(staging[len("BH102"):], "0")
		if numStr == "" {
			numStr = "0"
		}
		secNum, _ := strconv.Atoi(numStr)

		if pin, ok := gwbMap[secNum]; ok {
			allMappings = append(allMappings, secMapping{secNum, pin, "gwb-ref"})
			continue
		}

		// Fallback: text-based matching using English text
		rawHTML, hasText := enText[staging]
		if !hasText {
			allMappings = append(allMappings, secMapping{secNum, "", "no-text"})
			continue
		}
		rawText := stripHTML(rawHTML)
		normText := normalize(rawText)

		var bestPin string
		var bestScore int

		for _, inv := range ftEntries {
			score := commonPrefixLen(normText, inv.norm)
			if score > bestScore && score >= 30 {
				bestScore = score
				bestPin = inv.pin
			}
		}
		if bestScore < 40 {
			for _, inv := range flEntries {
				score := commonPrefixLen(normText, inv.norm)
				if score > bestScore && score >= 25 {
					bestScore = score
					bestPin = inv.pin
				}
			}
		}

		if bestPin != "" {
			allMappings = append(allMappings, secMapping{secNum, bestPin, fmt.Sprintf("text=%d", bestScore)})
		} else {
			allMappings = append(allMappings, secMapping{secNum, "", "unmatched"})
		}
	}

	// Count how many times each PIN appears to decide on suffixes
	pinCount := make(map[string]int)
	for _, m := range allMappings {
		if m.pin != "" {
			pinCount[m.pin]++
		}
	}

	// Second pass: build final mapping with GWB suffixes for disambiguation.
	// Add G+section_number suffix when multiple Gleanings excerpts come from
	// the same source tablet, to keep phelps codes unique.
	mapping := make(map[string]string)
	unmatched := 0
	gwbMatched := 0
	textMatched := 0

	for i, row := range dbRows {
		staging := row["phelps"]
		m := allMappings[i]

		if m.pin == "" {
			shortText := ""
			if raw, ok := enText[staging]; ok {
				shortText = stripHTML(raw)
				if len(shortText) > 80 {
					shortText = shortText[:80]
				}
			}
			fmt.Printf("  %s (GWB %d) → NO MATCH [%s...]\n", staging, m.secNum, shortText)
			unmatched++
			continue
		}

		finalCode := m.pin
		if pinCount[m.pin] > 1 {
			// Add GWB section suffix: e.g. BH00001G037
			suffix := fmt.Sprintf("G%03d", m.secNum)
			finalCode = m.pin + suffix
		}

		// Verify it fits in VARCHAR(16)
		if len(finalCode) > 16 {
			fmt.Fprintf(os.Stderr, "  WARNING: %s → %s exceeds 16 chars, truncating\n", staging, finalCode)
			finalCode = finalCode[:16]
		}

		mapping[staging] = finalCode
		if m.method == "gwb-ref" {
			gwbMatched++
		} else {
			textMatched++
		}
		fmt.Printf("  %s (GWB %d) → %s [%s]\n", staging, m.secNum, finalCode, m.method)
	}

	fmt.Printf("  Gleanings: %d matched (%d gwb-ref, %d text), %d unmatched\n",
		len(mapping), gwbMatched, textMatched, unmatched)
	return mapping
}

// commonPrefixLen returns the length of the common prefix between two normalized strings.
func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// ── SQL generation and application ──────────────────────────────────────────

func generateSQL(mapping map[string]string, prefix string) string {
	var sb strings.Builder
	sb.WriteString("SET FOREIGN_KEY_CHECKS=0;\n")
	for oldCode, newCode := range mapping {
		// Update all rows where phelps matches the old staging code exactly
		sb.WriteString(fmt.Sprintf(
			"UPDATE writings SET phelps='%s' WHERE phelps='%s';\n",
			sqlEsc(newCode), sqlEsc(oldCode),
		))
	}
	sb.WriteString("SET FOREIGN_KEY_CHECKS=1;\n")
	return sb.String()
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Print SQL but don't apply")
	doltDirFlag := flag.String("dolt-dir", doltDir, "Path to Dolt repo")
	flag.Parse()
	doltDir = *doltDirFlag

	// ── SAQ ──
	saqMapping := matchSAQ()
	if saqMapping != nil && len(saqMapping) > 0 {
		saqSQL := generateSQL(saqMapping, "AB")
		outFile := "/tmp/saq_remap.sql"
		os.WriteFile(outFile, []byte(saqSQL), 0644)
		fmt.Printf("\nSAQ SQL written to %s (%d mappings)\n", outFile, len(saqMapping))

		if !*dryRun {
			fmt.Println("Applying SAQ mappings...")
			err := doltExec(saqSQL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR applying SAQ SQL: %v\n", err)
			} else {
				fmt.Println("SAQ mappings applied successfully!")
			}
		}
	}

	// ── Gleanings ──
	glMapping := matchGleanings()
	if glMapping != nil && len(glMapping) > 0 {
		glSQL := generateSQL(glMapping, "BH")
		outFile := "/tmp/gleanings_remap.sql"
		os.WriteFile(outFile, []byte(glSQL), 0644)
		fmt.Printf("\nGleanings SQL written to %s (%d mappings)\n", outFile, len(glMapping))

		if !*dryRun {
			fmt.Println("Applying Gleanings mappings...")
			err := doltExec(glSQL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR applying Gleanings SQL: %v\n", err)
			} else {
				fmt.Println("Gleanings mappings applied successfully!")
			}
		}
	}

	// ── Summary ──
	fmt.Println("\n=== Summary ===")
	if saqMapping != nil {
		fmt.Printf("SAQ: %d chapters mapped\n", len(saqMapping))
	}
	if glMapping != nil {
		fmt.Printf("Gleanings: %d sections mapped\n", len(glMapping))
	}
}
