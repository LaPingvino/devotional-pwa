// gen_hugo_data.go — queries Dolt and writes JSON data files for Hugo build
//
// Usage:
//   go run gen_hugo_data.go [--dolt-dir ~/bahaiwritings] [--out-dir /path/to/hugo-site]
//
// Outputs (relative to out-dir):
//   data/languages.json           — [{code, name, prayer_count, rtl}, ...]
//   assets/prayers/{lang}.json    — [{phelps, text, name, category, cat_order, order_in_cat, translations}, ...]
//   assets/phelps/{base}.json     — {pin, subcodes:[{code, anchor, title, first_line, first_line_orig,
//                                     language, word_count, subjects, notes, translations:[{language,lang_name}]}]}
//   static/data/inventory.json    — [{pin, title, first_line, first_line_orig, language, word_count,
//                                     subjects, notes, prefix, translations}, ...]

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
	"sort"
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

// LangRef is a compact language reference (no text) used in translation lists
type LangRef struct {
	Language string `json:"language"`
	LangName string `json:"lang_name"`
}

// PrayerSource holds text from one source for the same prayer.
type PrayerSource struct {
	Source  string `json:"source"`
	Version string `json:"version"`
	Text    string `json:"text"`
	Notes   string `json:"notes,omitempty"`
}

// Prayer for per-language data files
type Prayer struct {
	Phelps        string         `json:"phelps"`
	Text          string         `json:"text"`
	Name          string         `json:"name,omitempty"`
	Category      string         `json:"category,omitempty"`
	CategoryOrder int            `json:"cat_order,omitempty"`
	OrderInCat    int            `json:"order_in_cat,omitempty"`
	Source        string         `json:"source,omitempty"`
	Version       string         `json:"version,omitempty"`
	Notes         string         `json:"notes,omitempty"`
	AltSources    []PrayerSource `json:"alt_sources,omitempty"` // additional sources for same prayer
	Translations  []LangRef      `json:"translations,omitempty"` // other languages with this phelps code
}

// SubCode is one passage within a base PIN (e.g. BH01313NAM within BH01313).
// Translations contains only language refs — prayer text lives in the per-language files.
type SubCode struct {
	Code          string    `json:"code"`
	Anchor        string    `json:"anchor"`                    // lowercase mnemonic suffix (e.g. "nam"), "" for base codes
	Title         string    `json:"title,omitempty"`
	FirstLine     string    `json:"first_line,omitempty"`      // English first line
	FirstLineOrig string    `json:"first_line_orig,omitempty"` // original-language first line
	Language      string    `json:"language,omitempty"`        // original language (Ara, Per, Eng, …)
	WordCount     string    `json:"word_count,omitempty"`
	Subjects      string    `json:"subjects,omitempty"`
	Notes         string    `json:"notes,omitempty"`
	FullTextParts []string  `json:"full_text_parts,omitempty"` // English full text chunks from inventory_fulltext
	Translations  []LangRef `json:"translations"` // languages that have this code; no text here
}

// PhelpsFile is written to assets/phelps/{base_pin}.json; groups all sub-codes.
type PhelpsFile struct {
	PIN      string    `json:"pin"`
	SubCodes []SubCode `json:"subcodes"`
}

// InventoryEntry for the concordance JSON served to the client
type InventoryEntry struct {
	PIN              string `json:"pin"`
	Title            string `json:"title,omitempty"`
	FirstLine        string `json:"first_line"`
	FirstLineOrig    string `json:"first_line_orig,omitempty"`
	Language         string `json:"language,omitempty"`
	WordCount        string `json:"word_count,omitempty"`
	Subjects         string `json:"subjects,omitempty"`
	Notes            string `json:"notes,omitempty"`
	Prefix           string `json:"prefix"`
	TranslationCount int    `json:"translations,omitempty"` // number of translated versions
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

	// 2a. First pass: collect all prayers and build deduped phelps→language index
	log.Println("→ prayers by language (pass 1: collecting)...")
	allPrayers := map[string][]Prayer{} // langCode → prayers
	phelpsLangs := map[string][]LangRef{} // phelps full code → deduped lang refs
	phelpsLangsSeen := map[string]map[string]bool{} // dedup tracker

	for _, lang := range langs {
		prayers := queryPrayersForLang(lang.Code)
		allPrayers[lang.Code] = prayers
		for _, p := range prayers {
			if p.Phelps == "" {
				continue
			}
			if phelpsLangsSeen[p.Phelps] == nil {
				phelpsLangsSeen[p.Phelps] = map[string]bool{}
			}
			if phelpsLangsSeen[p.Phelps][lang.Code] {
				continue // one entry per language per phelps code
			}
			phelpsLangsSeen[p.Phelps][lang.Code] = true
			phelpsLangs[p.Phelps] = append(phelpsLangs[p.Phelps], LangRef{
				Language: lang.Code,
				LangName: lang.Name,
			})
		}
		log.Printf("  %s: %d prayers", lang.Code, len(prayers))
	}

	// 2b. Second pass: write per-language JSON with "Also in:" translation lists
	log.Println("→ prayers by language (pass 2: writing with translation lists)...")
	for _, lang := range langs {
		prayers := allPrayers[lang.Code]
		for i, p := range prayers {
			if refs, ok := phelpsLangs[p.Phelps]; ok {
				// Exclude current language from its own translation list
				others := make([]LangRef, 0, len(refs)-1)
				for _, r := range refs {
					if r.Language != lang.Code {
						others = append(others, r)
					}
				}
				prayers[i].Translations = others
			}
		}
		writeJSON(filepath.Join(assetsDir, "prayers", lang.Code+".json"), prayers)
	}

	// 3. Group phelps codes by base PIN (strips trailing 3-char alpha mnemonic suffix)
	log.Println("→ grouping phelps codes by base PIN...")
	basePINMap := map[string][]string{} // basePin → sorted list of full codes (from prayers)
	for pin := range phelpsLangs {
		base := basePINKey(pin)
		basePINMap[base] = append(basePINMap[base], pin)
	}
	for base := range basePINMap {
		sort.Strings(basePINMap[base])
	}
	log.Printf("  %d base PINs from %d full codes", len(basePINMap), len(phelpsLangs))

	// 4. Inventory → static/data/ (JS search) + in-memory map
	log.Println("→ inventory...")
	inventory := queryInventory()
	log.Printf("  %d inventory entries", len(inventory))

	// Enrich inventory with translation counts and build lookup map
	invMap := map[string]InventoryEntry{}
	invBasePINMap := map[string][]string{} // basePin → sorted list of full inventory codes
	for i, e := range inventory {
		inventory[i].TranslationCount = len(phelpsLangs[e.PIN])
		invMap[e.PIN] = inventory[i]
		base := basePINKey(e.PIN)
		invBasePINMap[base] = append(invBasePINMap[base], e.PIN)
	}
	for base := range invBasePINMap {
		sort.Strings(invBasePINMap[base])
	}
	writeJSON(filepath.Join(staticDir, "inventory.json"), inventory)

	// Clear stale phelps files (keyed by base PIN, prayer-based only)
	phelpsDir := filepath.Join(assetsDir, "phelps")
	if entries, err := os.ReadDir(phelpsDir); err == nil {
		for _, e := range entries {
			base := strings.ToUpper(strings.TrimSuffix(e.Name(), ".json"))
			if _, ok := basePINMap[base]; !ok {
				os.Remove(filepath.Join(phelpsDir, e.Name()))
			}
		}
	}

	// 5. Write phelps files grouped by base PIN (lang refs only, no prayer text)
	// Only generate static pages for PINs that have at least one matching prayer.
	// Cloudflare Pages has a 20K file limit; inventory-only PINs are served via
	// the inventory search (/phelps/?pin=XX) instead of individual static pages.
	log.Println("→ loading inventory fulltext (English reference text chunks)...")
	fullTexts := queryFullText()
	log.Printf("  %d fulltext entries", len(fullTexts))

	log.Println("→ writing phelps files grouped by base PIN...")
	for base, codes := range basePINMap {
		// Also include any inventory-only sub-codes under this same base PIN
		codeSet := map[string]bool{}
		for _, c := range codes {
			codeSet[c] = true
		}
		for _, c := range invBasePINMap[base] {
			codeSet[c] = true
		}
		allCodes := make([]string, 0, len(codeSet))
		for c := range codeSet {
			allCodes = append(allCodes, c)
		}
		sort.Strings(allCodes)

		var subcodes []SubCode
		for _, code := range allCodes {
			inv := invMap[code]
			anchor := strings.ToLower(strings.TrimPrefix(code, base))
			trans := phelpsLangs[code]
			if trans == nil {
				trans = []LangRef{}
			}
			subcodes = append(subcodes, SubCode{
				Code:          code,
				Anchor:        anchor,
				Title:         inv.Title,
				FirstLine:     inv.FirstLine,
				FirstLineOrig: inv.FirstLineOrig,
				Language:      inv.Language,
				WordCount:     inv.WordCount,
				Subjects:      inv.Subjects,
				Notes:         inv.Notes,
				FullTextParts: fullTexts[code],
				Translations:  trans,
			})
		}
		pf := PhelpsFile{
			PIN:      base,
			SubCodes: subcodes,
		}
		safe := strings.ToLower(base)
		writeJSON(filepath.Join(assetsDir, "phelps", safe+".json"), pf)
	}

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
		SELECT l.langcode, l.name, COUNT(DISTINCT w.phelps) as cnt
		FROM languages l
		LEFT JOIN writings w ON w.language = l.langcode
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
	// Prefer bahaiprayers.net as primary (sort 0), bahaiprayers.app as secondary (sort 1)
	rows := doltQuery(fmt.Sprintf(`
		SELECT w.phelps, w.text, COALESCE(w.name,''), w.source, w.version, COALESCE(w.notes,''),
		       COALESCE(pbs.category_name,''),
		       COALESCE(pbs.category_order,0),
		       COALESCE(pbs.order_in_category,0)
		FROM writings w
		LEFT JOIN prayer_book_structure pbs
		    ON pbs.phelps_code = w.phelps
		    AND pbs.source_language = '%s'
		WHERE w.language = '%s'
		    AND w.phelps IS NOT NULL AND w.phelps <> ''
		ORDER BY CASE w.source WHEN 'bahaiprayers.net' THEN 0 ELSE 1 END,
		         COALESCE(pbs.category_order,9999),
		         COALESCE(pbs.order_in_category,9999),
		         w.phelps
	`, safe, safe))

	type rawRow struct {
		phelps, text, name, source, version, notes string
		catName                                    string
		catOrd, ordInCat                           int
	}

	// Group by phelps: first row becomes primary, rest become alt_sources
	type group struct {
		primary rawRow
		alts    []PrayerSource
	}
	groups := map[string]*group{}
	var order []string // insertion order = category-sorted order from primary rows

	for _, row := range rows[1:] {
		if len(row) < 9 {
			continue
		}
		catOrd, ordInCat := 0, 0
		fmt.Sscanf(row[7], "%d", &catOrd)
		fmt.Sscanf(row[8], "%d", &ordInCat)
		r := rawRow{
			phelps: row[0], text: row[1], name: row[2],
			source: row[3], version: row[4], notes: row[5],
			catName: row[6], catOrd: catOrd, ordInCat: ordInCat,
		}
		if g, ok := groups[r.phelps]; !ok {
			groups[r.phelps] = &group{primary: r}
			order = append(order, r.phelps)
		} else if r.source == "bahaiprayers.net" && g.primary.source != "bahaiprayers.net" {
			// Promote net to primary if app was first
			g.alts = append([]PrayerSource{{
				Source: g.primary.source, Version: g.primary.version,
				Text: g.primary.text, Notes: g.primary.notes,
			}}, g.alts...)
			g.primary = r
		} else if r.source != g.primary.source {
			// Only add as alt if it's a genuinely different source
			// (skip duplicates from the same source caused by prayer_book_structure multi-category join)
			alreadyHaveSource := false
			for _, a := range g.alts {
				if a.Source == r.source {
					alreadyHaveSource = true
					break
				}
			}
			if !alreadyHaveSource {
				g.alts = append(g.alts, PrayerSource{
					Source: r.source, Version: r.version, Text: r.text, Notes: r.notes,
				})
			}
		}
	}

	out := make([]Prayer, 0, len(order))
	for _, phelps := range order {
		g := groups[phelps]
		p := Prayer{
			Phelps:        phelps,
			Text:          g.primary.text,
			Name:          g.primary.name,
			Category:      g.primary.catName,
			CategoryOrder: g.primary.catOrd,
			OrderInCat:    g.primary.ordInCat,
			Source:        g.primary.source,
			Version:       g.primary.version,
			Notes:         g.primary.notes,
		}
		if len(g.alts) > 0 {
			p.AltSources = g.alts
		}
		out = append(out, p)
	}
	return out
}

func queryInventory() []InventoryEntry {
	rows := doltQuery(`SELECT PIN,
		COALESCE(Title,''),
		COALESCE(` + "`First line (translated)`" + `,''),
		COALESCE(` + "`First line (original)`" + `,''),
		COALESCE(Language,''),
		COALESCE(` + "`Word count`" + `,''),
		COALESCE(Subjects,''),
		COALESCE(Notes,''),
		COALESCE(prefix,'')
		FROM inventory ORDER BY PIN`)
	var out []InventoryEntry
	for _, row := range rows[1:] {
		if len(row) < 9 {
			continue
		}
		out = append(out, InventoryEntry{
			PIN:           row[0],
			Title:         row[1],
			FirstLine:     row[2],
			FirstLineOrig: row[3],
			Language:      row[4],
			WordCount:     row[5],
			Subjects:      row[6],
			Notes:         row[7],
			Prefix:        row[8],
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

// queryFullText returns a map of phelps code → ordered text chunks (English)
// from the inventory_fulltext table, kept as separate parts for paginated display.
func queryFullText() map[string][]string {
	rows := doltQuery(`
		SELECT phelps, part, text
		FROM inventory_fulltext
		WHERE language = 'en'
		ORDER BY phelps, part
	`)
	type chunk struct {
		part int
		text string
	}
	chunks := map[string][]chunk{}
	for _, row := range rows[1:] {
		if len(row) < 3 {
			continue
		}
		var p int
		fmt.Sscanf(row[1], "%d", &p)
		chunks[row[0]] = append(chunks[row[0]], chunk{p, row[2]})
	}
	out := map[string][]string{}
	for pin, cs := range chunks {
		sort.Slice(cs, func(i, j int) bool { return cs[i].part < cs[j].part })
		parts := make([]string, len(cs))
		for i, c := range cs {
			parts[i] = c.text
		}
		out[pin] = parts
	}
	return out
}

// basePINKey strips a trailing 3-char alpha mnemonic suffix from a Phelps code.
// BH01313NAM → BH01313, AB04427GUI → AB04427, BH05849 → BH05849 (unchanged).
func basePINKey(pin string) string {
	n := len(pin)
	if n < 4 {
		return pin
	}
	suffix := pin[n-3:]
	for _, c := range suffix {
		if c < 'A' || c > 'Z' {
			return pin
		}
	}
	// Confirm char before suffix is a digit (part of the numeric ID, not a prefix letter)
	if pin[n-4] >= '0' && pin[n-4] <= '9' {
		return pin[:n-3]
	}
	return pin
}
