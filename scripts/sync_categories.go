// sync_categories.go — Populate prayer_book_structure from bahaiprayers.net API.
//
// Fetches prayers for a given language from the API, matches them to phelps
// codes in the DB via source_id, and inserts category→phelps mappings into
// prayer_book_structure.
//
// Usage:
//   go run sync_categories.go --lang en          # sync English categories (API lang ID auto-detected)
//   go run sync_categories.go --langid 1         # sync by API language ID
//   go run sync_categories.go --lang en --clear  # clear existing before inserting
//   go run sync_categories.go --all              # sync all languages present in lang.csv
//
// Output:
//   SQL statements written to /tmp/sync_categories_{lang}.sql
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)


const (
	catApiBase = "https://BahaiPrayers.net/api/prayer/"
	catDoltDir = "/home/joop/prayermatching/bahaiwritings"
)

type CatAPILanguage struct {
	Id          int    `json:"Id"`
	English     string `json:"English"`
	PrayerCount int    `json:"PrayerCount"`
}

type CatAPIPrayer struct {
	Id           int    `json:"Id"`
	AuthorId     int    `json:"AuthorId"`
	LanguageId   int    `json:"LanguageId"`
	Text         string `json:"Text"`
	FirstTagName string `json:"FirstTagName"`
}

type CatAPIPrayerBook struct {
	Prayers []CatAPIPrayer `json:"Prayers"`
}

type CatLangInfo struct {
	APIID int
	ISO   string
	Name  string
}

func catGetJSON(url string, target interface{}) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func catFetchLanguages() []CatAPILanguage {
	var langs []CatAPILanguage
	if err := catGetJSON(catApiBase+"Languages", &langs); err != nil {
		panic(err)
	}
	return langs
}

func catFetchPrayers(langID int) []CatAPIPrayer {
	url := fmt.Sprintf("%sprayersystembylanguage?html=true&languageid=%d", catApiBase, langID)
	var book CatAPIPrayerBook
	if err := catGetJSON(url, &book); err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: fetch failed for lang %d: %v\n", langID, err)
		return nil
	}
	return book.Prayers
}

func catLoadLangAPIIDs() map[string]int {
	// Returns iso → API ID mapping from dolt languages table
	cmd := exec.Command("dolt", "sql", "-q",
		"SELECT langcode, api_id FROM languages WHERE api_id IS NOT NULL",
		"--result-format", "csv")
	cmd.Dir = catDoltDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: Could not load api_ids from dolt: %v\n", err)
		return nil
	}
	m := make(map[string]int)
	r := csv.NewReader(strings.NewReader(string(out)))
	rows, _ := r.ReadAll()
	for _, row := range rows[1:] { // skip header
		if len(row) < 2 {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(row[1]))
		if err != nil {
			continue
		}
		m[strings.TrimSpace(row[0])] = id
	}
	return m
}

// loadDBPhelps returns a map of source_id → phelps for a given ISO language.
func loadDBPhelps(iso string) map[int]string {
	query := fmt.Sprintf(
		"SELECT source_id, phelps FROM writings WHERE source='bahaiprayers.net' AND language='%s' AND phelps IS NOT NULL AND phelps <> ''",
		iso,
	)
	cmd := exec.Command("dolt", "sql", "-q", query, "--result-format", "csv")
	cmd.Dir = catDoltDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  WARN: DB query failed for %s: %v\n%s\n", iso, err, string(out))
		return nil
	}
	m := make(map[int]string)
	r := csv.NewReader(strings.NewReader(string(out)))
	rows, _ := r.ReadAll()
	for _, row := range rows[1:] { // skip header
		if len(row) < 2 {
			continue
		}
		id, err := strconv.Atoi(row[0])
		if err != nil {
			continue
		}
		m[id] = strings.TrimSpace(row[1])
	}
	return m
}

func sqlEsc(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	return s
}

func syncLang(iso string, apiID int, clearFirst bool) []string {
	fmt.Printf("Syncing %s (API ID %d)...\n", iso, apiID)

	prayers := catFetchPrayers(apiID)
	if len(prayers) == 0 {
		fmt.Printf("  No prayers returned from API\n")
		return nil
	}
	fmt.Printf("  %d prayers from API\n", len(prayers))

	dbPhelps := loadDBPhelps(iso)
	fmt.Printf("  %d phelps codes in DB for %s\n", len(dbPhelps), iso)

	var stmts []string

	if clearFirst {
		stmts = append(stmts, fmt.Sprintf(
			"DELETE FROM prayer_book_structure WHERE source_language='%s';", sqlEsc(iso),
		))
	}

	type Entry struct {
		phelps        string
		category      string
		categoryOrder int
		withinOrder   int
	}

	categoryOrder := map[string]int{}
	catOrderCounter := 0
	withinCatCounter := map[string]int{}
	inserted := map[string]bool{} // key: "phelps|category" to avoid dupes

	matched, skipped := 0, 0
	for _, p := range prayers {
		phelps, ok := dbPhelps[p.Id]
		if !ok {
			skipped++
			continue
		}
		tag := strings.TrimSpace(p.FirstTagName)
		if tag == "" {
			tag = "Uncategorized"
		}

		// Track category order (first time we see a category)
		if _, seen := categoryOrder[tag]; !seen {
			categoryOrder[tag] = catOrderCounter
			catOrderCounter++
		}

		withinCatCounter[tag]++
		key := phelps + "|" + tag
		if inserted[key] {
			continue // same phelps may appear multiple times in same category
		}
		inserted[key] = true

		stmts = append(stmts, fmt.Sprintf(
			"INSERT INTO prayer_book_structure (source_language, category_name, phelps_code, category_order, order_in_category) VALUES ('%s', '%s', '%s', %d, %d) ON DUPLICATE KEY UPDATE category_order=%d, order_in_category=%d;",
			sqlEsc(iso), sqlEsc(tag), sqlEsc(phelps),
			categoryOrder[tag], withinCatCounter[tag],
			categoryOrder[tag], withinCatCounter[tag],
		))
		matched++
	}

	fmt.Printf("  %d matched, %d skipped (no phelps in DB)\n", matched, skipped)
	return stmts
}

func main() {
	langFlag := flag.String("lang", "", "ISO language code (e.g. en, fr, de)")
	langIDFlag := flag.Int("langid", 0, "API language ID")
	clearFlag := flag.Bool("clear", false, "Clear existing entries for this language before inserting")
	allFlag := flag.Bool("all", false, "Sync all languages from lang.csv")
	flag.Parse()

	isoToAPIID := catLoadLangAPIIDs()

	// If --all, collect all (iso, apiID) pairs
	type LangPair struct{ ISO string; APIID int }
	var targets []LangPair

	if *allFlag {
		for iso, id := range isoToAPIID {
			targets = append(targets, LangPair{iso, id})
		}
	} else if *langFlag != "" {
		iso := *langFlag
		apiID := isoToAPIID[iso]
		if apiID == 0 && *langIDFlag == 0 {
			// Try fetching from API to find the language
			langs := catFetchLanguages()
			// Find by English name or ISO match
			for _, l := range langs {
				if strings.EqualFold(l.English, iso) {
					apiID = l.Id
					break
				}
			}
		}
		if *langIDFlag != 0 {
			apiID = *langIDFlag
		}
		if apiID == 0 {
			fmt.Fprintf(os.Stderr, "ERROR: Could not find API ID for language %q. Use --langid to specify.\n", iso)
			os.Exit(1)
		}
		targets = append(targets, LangPair{iso, apiID})
	} else if *langIDFlag != 0 {
		// Look up ISO from CSV
		iso := ""
		for k, v := range isoToAPIID {
			if v == *langIDFlag {
				iso = k
				break
			}
		}
		if iso == "" {
			iso = fmt.Sprintf("lang%d", *langIDFlag)
		}
		targets = append(targets, LangPair{iso, *langIDFlag})
	} else {
		flag.Usage()
		os.Exit(1)
	}

	// Also add source_language = 'en' as the default/primary structure
	// by ensuring en is in targets (if --all, it's already there)

	var allStmts []string
	for _, t := range targets {
		stmts := syncLang(t.ISO, t.APIID, *clearFlag)
		if len(stmts) > 0 {
			allStmts = append(allStmts, fmt.Sprintf("\n-- %s (API ID %d)", t.ISO, t.APIID))
			allStmts = append(allStmts, stmts...)
		}
	}

	if len(allStmts) == 0 {
		fmt.Println("No statements generated.")
		return
	}

	// Write to SQL file
	var outFile string
	if *allFlag {
		outFile = "/tmp/sync_categories_all.sql"
	} else {
		outFile = fmt.Sprintf("/tmp/sync_categories_%s.sql", (*langFlag + fmt.Sprintf("%d", *langIDFlag)))
	}

	f, err := os.Create(outFile)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	for _, s := range allStmts {
		fmt.Fprintln(f, s)
	}
	fmt.Printf("\nWrote %d statements to %s\n", len(allStmts), outFile)
	fmt.Printf("Apply with: grep '^INSERT\\|^DELETE' %s | dolt sql\n", outFile)
}
