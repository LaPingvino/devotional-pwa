// categorize_en.go — Assign English prayerbook categories to uncategorized prayers
// by mapping from other-language prayerbook categories.
//
// Usage:
//   go run ~/prayermatching/scripts/categorize_en.go | grep "^INSERT" | dolt sql
//   go run ~/prayermatching/scripts/categorize_en.go --apply    # run inserts directly
//
// Strategy:
//   1. Build (other_lang, other_cat) → en_cat mapping from prayers in BOTH books.
//   2. For each uncategorized prayer that has an entry in another prayerbook,
//      look up the mapped English category and insert it.
//   3. For prayers with no prayerbook entry at all, infer from phelps prefix.

package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

var (
	doltDir = flag.String("db", os.ExpandEnv("$HOME/bahaiwritings"), "Dolt repo path")
	apply   = flag.Bool("apply", false, "Apply INSERTs directly instead of printing")
	source  = flag.String("source", "llm-translation", "Source to categorize (or 'all')")
)

// Ordered list of reference languages for the mapping (closest to English first)
var refLangs = []string{
	"nl", "de", "fr", "es", "pt", "no", "da", "sv", "fi", "it", "pl", "ro",
	"ca", "az", "ja", "is", "el", "ht", "bg", "tl", "da", "uk", "ko",
	"zh-Hans", "sk", "tvl", "hu", "ru", "ml", "ar", "hi", "hy", "ms",
	"id", "lv", "af", "bs", "th", "sq", "vi", "eo", "ur", "mg", "mn",
	"zh-Hant", "kn", "ne", "iba", "kl", "fa",
}

func doltQuery(query string) [][]string {
	cmd := exec.Command("dolt", "sql", "-q", query, "--result-format", "csv")
	cmd.Dir = *doltDir
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("dolt error: %v\nQuery: %s", err, query)
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		log.Fatalf("csv: %v", err)
	}
	return rows
}

func doltExec(query string) {
	cmd := exec.Command("dolt", "sql", "-q", query)
	cmd.Dir = *doltDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("dolt exec error: %v\nQuery: %s", err, query)
	}
}

func main() {
	flag.Parse()

	// 1. Build cross-language category mapping from prayers in BOTH English and ref prayerbooks
	fmt.Fprintln(os.Stderr, "Building cross-language category mapping...")
	// PBS source_language values now carry the :bp suffix; suffix the ref-list to match.
	refLangsBp := make([]string, len(refLangs))
	for i, l := range refLangs {
		refLangsBp[i] = l + ":bp"
	}
	refLangList := "'" + strings.Join(refLangsBp, "','") + "'"
	mapRows := doltQuery(fmt.Sprintf(`
		SELECT pbs_en.category_name, pbs_en.category_order,
		       pbs_other.source_language, pbs_other.category_name, COUNT(*) as cnt
		FROM prayer_book_structure pbs_en
		JOIN prayer_book_structure pbs_other
		    ON pbs_other.phelps_code = pbs_en.phelps_code
		    AND pbs_other.source_language IN (%s)
		WHERE pbs_en.source_language = 'en:bp'
		GROUP BY pbs_en.category_name, pbs_en.category_order,
		         pbs_other.source_language, pbs_other.category_name
		ORDER BY pbs_other.source_language, cnt DESC
	`, refLangList))

	// mapping[lang][other_cat] = (en_cat, en_order) — first/most common wins
	type enCatEntry struct{ name string; order int }
	mapping := map[string]map[string]enCatEntry{}
	for _, row := range mapRows[1:] {
		if len(row) < 4 {
			continue
		}
		enCat, lang, otherCat := row[0], row[2], row[3]
		enOrd, _ := strconv.Atoi(row[1])
		if mapping[lang] == nil {
			mapping[lang] = map[string]enCatEntry{}
		}
		if _, exists := mapping[lang][otherCat]; !exists {
			mapping[lang][otherCat] = enCatEntry{name: enCat, order: enOrd}
		}
	}
	total := 0
	for _, m := range mapping {
		total += len(m)
	}
	fmt.Fprintf(os.Stderr, "  %d category mappings across %d languages\n", total, len(mapping))

	// 2. Get current max order_in_category per English category
	maxOrdRows := doltQuery(`
		SELECT category_name, COALESCE(MAX(order_in_category), 0)
		FROM prayer_book_structure WHERE source_language = 'en:bp'
		GROUP BY category_name
	`)
	catMaxOrd := map[string]int{}
	catOrder := map[string]int{}
	for _, row := range maxOrdRows[1:] {
		if len(row) < 2 {
			continue
		}
		ord, _ := strconv.Atoi(row[1])
		catMaxOrd[row[0]] = ord
	}

	// Also capture category_order values
	enCatRows := doltQuery(`
		SELECT DISTINCT category_name, category_order
		FROM prayer_book_structure WHERE source_language = 'en:bp'
		ORDER BY category_order
	`)
	for _, row := range enCatRows[1:] {
		if len(row) < 2 {
			continue
		}
		ord, _ := strconv.Atoi(row[1])
		catOrder[row[0]] = ord
	}

	// 3. Find uncovered prayers with other-prayerbook entries
	sourceFilter := ""
	if *source != "all" {
		sourceFilter = fmt.Sprintf("AND w.source = '%s'", strings.ReplaceAll(*source, "'", "''"))
	}
	uncoveredRows := doltQuery(fmt.Sprintf(`
		SELECT DISTINCT w.phelps, pbs_other.source_language, pbs_other.category_name
		FROM writings w
		JOIN prayer_book_structure pbs_other
		    ON pbs_other.phelps_code = w.phelps
		    AND pbs_other.source_language IN (%s)
		LEFT JOIN prayer_book_structure pbs_en
		    ON pbs_en.phelps_code = w.phelps AND pbs_en.source_language = 'en:bp'
		WHERE w.phelps IS NOT NULL AND w.phelps <> ''
		    AND pbs_en.phelps_code IS NULL
		    %s
		ORDER BY w.phelps, pbs_other.source_language
	`, refLangList, sourceFilter))

	// Build phelps → best (en_cat, en_order) from other prayerbooks
	// Prefer languages earlier in refLangs list
	langPriority := map[string]int{}
	for i, l := range refLangs {
		langPriority[l] = i
	}

	type phelpsEntry struct {
		enCat    string
		enOrd    int
		langPrio int
	}
	byCat := map[string]phelpsEntry{} // phelps → best match

	for _, row := range uncoveredRows[1:] {
		if len(row) < 3 {
			continue
		}
		phelps, lang, otherCat := row[0], row[1], row[2]
		langMap, ok := mapping[lang]
		if !ok {
			continue
		}
		enEntry, ok := langMap[otherCat]
		if !ok {
			continue
		}
		prio := langPriority[lang]
		cur, exists := byCat[phelps]
		if !exists || prio < cur.langPrio {
			byCat[phelps] = phelpsEntry{enCat: enEntry.name, enOrd: enEntry.order, langPrio: prio}
		}
	}
	fmt.Fprintf(os.Stderr, "  %d prayers can be categorized via cross-language mapping\n", len(byCat))

	// 4. Handle prayers with NO prayerbook entry at all (fallback: infer from phelps prefix)
	noBookRows := doltQuery(fmt.Sprintf(`
		SELECT DISTINCT w.phelps
		FROM writings w
		LEFT JOIN prayer_book_structure pbs ON pbs.phelps_code = w.phelps
		WHERE w.phelps IS NOT NULL AND w.phelps <> ''
		    AND pbs.phelps_code IS NULL
		    %s
	`, sourceFilter))

	type fallbackEntry struct {
		cat string
		ord int
	}
	fallbackCats := map[string]fallbackEntry{
		"AB": {"Other prayers by \u2018Abdu\u2019l-Bah\u00e1 (research)", 61},
		"BH": {"Other prayers by Bah\u00e1\u2019u\u2019ll\u00e1h (research)", 62},
		"BB": {"Other prayers by the B\u00e1b (research)", 63},
	}
	for _, row := range noBookRows[1:] {
		if len(row) < 1 {
			continue
		}
		phelps := row[0]
		if _, exists := byCat[phelps]; exists {
			continue // already mapped
		}
		prefix := ""
		if len(phelps) >= 2 {
			prefix = strings.ToUpper(phelps[:2])
		}
		fb, ok := fallbackCats[prefix]
		if !ok {
			fb = fallbackCats["BH"] // default to Bahá'u'lláh bucket
		}
		byCat[phelps] = phelpsEntry{enCat: fb.cat, enOrd: fb.ord, langPrio: 999}
	}
	fmt.Fprintf(os.Stderr, "  %d total prayers to categorize (including prefix-fallback)\n", len(byCat))

	// 5. Generate INSERT statements in a stable order
	phelpsKeys := make([]string, 0, len(byCat))
	for p := range byCat {
		phelpsKeys = append(phelpsKeys, p)
	}
	sort.Strings(phelpsKeys)

	var inserts []string
	for _, phelps := range phelpsKeys {
		entry := byCat[phelps]
		catMaxOrd[entry.enCat]++
		ordInCat := catMaxOrd[entry.enCat]
		safeP := strings.ReplaceAll(phelps, "'", "''")
		safeCat := strings.ReplaceAll(entry.enCat, "'", "''")
		ins := fmt.Sprintf(
			"INSERT IGNORE INTO prayer_book_structure (phelps_code, source_language, category_name, category_order, order_in_category) VALUES ('%s', 'en:bp', '%s', %d, %d);",
			safeP, safeCat, entry.enOrd, ordInCat)
		inserts = append(inserts, ins)
	}

	if *apply {
		for _, ins := range inserts {
			doltExec(ins)
		}
		fmt.Fprintf(os.Stderr, "Applied %d inserts\n", len(inserts))
	} else {
		for _, ins := range inserts {
			fmt.Println(ins)
		}
	}
}
