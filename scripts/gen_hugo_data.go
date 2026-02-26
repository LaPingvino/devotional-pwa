// gen_hugo_data.go — queries Dolt and writes JSON data files for Hugo build
//
// Usage:
//   go run gen_hugo_data.go [--dolt-dir ~/bahaiwritings] [--out-dir /path/to/hugo-site]
//
// Outputs (relative to out-dir):
//   data/languages.json           — [{code, name, prayer_count, rtl}, ...]
//   data/prayers/{lang}.json      — [{phelps, text, name, category, category_order, order_in_cat}, ...]
//   data/phelps/{code}.json       — [{phelps, language, lang_name, text, name}, ...]
//   data/inventory.json           — [{pin, first_line, prefix}, ...]

package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	doltDir = flag.String("dolt-dir", filepath.Join(os.Getenv("HOME"), "bahaiwritings"), "Dolt repo path")
	outDir  = flag.String("out-dir", "/tmp/devotional-pwa", "Hugo site root (data/ written inside)")
)

// Language info
type Language struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	PrayerCount int    `json:"prayer_count"`
	RTL         bool   `json:"rtl"`
}

// Prayer for per-language data files (no language field needed)
type Prayer struct {
	Phelps        string `json:"phelps"`
	Text          string `json:"text"`
	Name          string `json:"name,omitempty"`
	Category      string `json:"category,omitempty"`
	CategoryOrder int    `json:"cat_order,omitempty"`
	OrderInCat    int    `json:"order_in_cat,omitempty"`
}

// Translation for per-phelps cross-language data files
type Translation struct {
	Language string `json:"language"`
	LangName string `json:"lang_name"`
	Text     string `json:"text"`
	Name     string `json:"name,omitempty"`
}

// InventoryEntry for concordance
type InventoryEntry struct {
	PIN       string `json:"pin"`
	FirstLine string `json:"first_line"`
	Prefix    string `json:"prefix"`
}

var rtlLangs = map[string]bool{
	"ar": true, "fa": true, "ur": true, "he": true, "ug": true,
}

func main() {
	flag.Parse()
	log.Printf("Dolt repo: %s", *doltDir)
	log.Printf("Hugo site: %s", *outDir)

	dataDir := filepath.Join(*outDir, "data")
	assetsDir := filepath.Join(*outDir, "assets")
	staticDir := filepath.Join(*outDir, "static", "data")
	for _, dir := range []string{
		dataDir,
		filepath.Join(assetsDir, "prayers"),
		filepath.Join(assetsDir, "phelps"),
		staticDir,
	} {
		must(os.MkdirAll(dir, 0755))
	}

	// 1. Languages
	log.Println("→ languages...")
	langs := queryLanguages()
	writeJSON(filepath.Join(dataDir, "languages.json"), langs)
	log.Printf("  %d languages", len(langs))

	// Build a name lookup for translations
	langNames := map[string]string{}
	for _, l := range langs {
		langNames[l.Code] = l.Name
	}

	// 2. Prayers per language + collect phelps translations
	log.Println("→ prayers by language...")
	phelpsMap := map[string][]Translation{} // phelps → translations

	for _, lang := range langs {
		prayers := queryPrayersForLang(lang.Code)
		writeJSON(filepath.Join(assetsDir, "prayers", lang.Code+".json"), prayers)

		for _, p := range prayers {
			if p.Phelps == "" {
				continue
			}
			phelpsMap[p.Phelps] = append(phelpsMap[p.Phelps], Translation{
				Language: lang.Code,
				LangName: lang.Name,
				Text:     p.Text,
				Name:     p.Name,
			})
		}
		log.Printf("  %s: %d prayers", lang.Code, len(prayers))
	}

	// 3. Phelps cross-language files (assets/, read lazily at build time)
	log.Printf("→ phelps translations (%d unique codes)...", len(phelpsMap))
	for pin, translations := range phelpsMap {
		safe := strings.ToLower(pin)
		writeJSON(filepath.Join(assetsDir, "phelps", safe+".json"), translations)
	}

	// 4. Inventory → static/ (served to client for JS search)
	log.Println("→ inventory...")
	inventory := queryInventory()
	writeJSON(filepath.Join(staticDir, "inventory.json"), inventory)
	log.Printf("  %d inventory entries", len(inventory))

	log.Println("Done!")
}

func doltQuery(query string) [][]string {
	cmd := exec.Command("dolt", "sql", "-q", query, "--result-format", "csv")
	cmd.Dir = *doltDir
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("dolt query failed: %v\nQuery: %s", err, query)
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		log.Fatalf("csv parse: %v", err)
	}
	return rows
}

func queryLanguages() []Language {
	rows := doltQuery(`
		SELECT l.langcode, l.name, COUNT(w.version) as cnt
		FROM languages l
		LEFT JOIN writings w ON w.language = l.langcode
		    AND w.source = 'bahaiprayers.net'
		    AND w.phelps IS NOT NULL AND w.phelps <> ''
		WHERE l.inlang = 'en'
		GROUP BY l.langcode, l.name
		HAVING cnt > 0
		ORDER BY l.name
	`)
	var out []Language
	for _, row := range rows[1:] {
		if len(row) < 3 {
			continue
		}
		cnt := 0
		fmt.Sscanf(row[2], "%d", &cnt)
		out = append(out, Language{
			Code:        row[0],
			Name:        row[1],
			PrayerCount: cnt,
			RTL:         rtlLangs[row[0]],
		})
	}
	return out
}

func queryPrayersForLang(lang string) []Prayer {
	safe := strings.ReplaceAll(lang, "'", "''")
	rows := doltQuery(fmt.Sprintf(`
		SELECT w.phelps, w.text, COALESCE(w.name,''),
		       COALESCE(pbs.category_name,''),
		       COALESCE(pbs.category_order,0),
		       COALESCE(pbs.order_in_category,0)
		FROM writings w
		LEFT JOIN prayer_book_structure pbs
		    ON pbs.phelps_code = w.phelps
		    AND pbs.source_language = '%s'
		WHERE w.language = '%s'
		    AND w.source = 'bahaiprayers.net'
		    AND w.phelps IS NOT NULL AND w.phelps <> ''
		ORDER BY COALESCE(pbs.category_order,9999),
		         COALESCE(pbs.order_in_category,9999),
		         w.phelps
	`, safe, safe))

	var out []Prayer
	for _, row := range rows[1:] {
		if len(row) < 6 {
			continue
		}
		catOrd, ordInCat := 0, 0
		fmt.Sscanf(row[4], "%d", &catOrd)
		fmt.Sscanf(row[5], "%d", &ordInCat)
		out = append(out, Prayer{
			Phelps:        row[0],
			Text:          row[1],
			Name:          row[2],
			Category:      row[3],
			CategoryOrder: catOrd,
			OrderInCat:    ordInCat,
		})
	}
	return out
}

func queryInventory() []InventoryEntry {
	rows := doltQuery("SELECT PIN, `First line (translated)`, prefix FROM inventory ORDER BY PIN")
	var out []InventoryEntry
	for _, row := range rows[1:] {
		if len(row) < 3 {
			continue
		}
		out = append(out, InventoryEntry{
			PIN:       row[0],
			FirstLine: row[1],
			Prefix:    row[2],
		})
	}
	return out
}

func writeJSON(path string, v any) {
	f, err := os.Create(path)
	must(err)
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(v); err != nil {
		log.Fatalf("json encode %s: %v", path, err)
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
