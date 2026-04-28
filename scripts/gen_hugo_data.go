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
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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

// BookRef names a prayerbook available for a language page
type BookRef struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Count int    `json:"count,omitempty"` // total prayer entries in this book
}

// BookCat holds one prayerbook's category assignment for a prayer
type BookCat struct {
	Category   string `json:"cat"`
	CatOrder   int    `json:"cat_order,omitempty"`
	OrderInCat int    `json:"order_in_cat,omitempty"`
}

// LangFile is the top-level structure written to assets/prayers/{lang}.json
type LangFile struct {
	Prayers     []Prayer  `json:"prayers"`
	Prayerbooks []BookRef `json:"prayerbooks,omitempty"`
	// DefaultBook is the prayerbook code the page should select on first
	// load. It's resolved data-side so the template doesn't need to repeat
	// the fallback chain. Order: own-language :bpnet → en:bpnet → first book.
	DefaultBook string `json:"default_book,omitempty"`
}

// PrayerSource holds text from one source for the same prayer.
type PrayerSource struct {
	Source  string `json:"source"`
	Version string `json:"version"`
	Text    string `json:"text"`
	Notes   string `json:"notes,omitempty"`
}

// uuidToBase36 converts a canonical UUID string (36 chars with hyphens)
// to its base36 representation (~25 chars). Used for compact /p/?v=
// permalinks. Mirrors the JS implementation in static/js/uuid-base36.js.
// Returns the input unchanged if it's not a valid UUID.
func uuidToBase36(uuid string) string {
	hex := strings.ReplaceAll(uuid, "-", "")
	if len(hex) != 32 {
		return uuid
	}
	n := new(big.Int)
	if _, ok := n.SetString(hex, 16); !ok {
		return uuid
	}
	return n.Text(36)
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
	// VersionB36 is the base36-encoded form of Version, used by templates
	// to build short /p/?v=<b36> permalinks without runtime conversion.
	VersionB36    string         `json:"v,omitempty"`
	Notes         string         `json:"notes,omitempty"`
	// Book is the prayerbook this prayer's native PBS entry belongs to
	// (e.g. "mul-NA:bp" for an Otjiherero prayer in the Namibian compilation).
	// Used to compute the default book for the language; also surfaced to
	// the client so /p/?v= can show "Part of: <book>" navigation.
	Book          string             `json:"book,omitempty"`
	AltSources    []PrayerSource     `json:"alt_sources,omitempty"`  // additional sources for same prayer
	BookCats      map[string]BookCat `json:"book_cats,omitempty"`    // prayerbook code → category assignment
	Translations  []LangRef          `json:"translations,omitempty"` // other languages with this phelps code
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
	FullTextParts []string      `json:"full_text_parts,omitempty"` // English full text chunks from inventory_fulltext
	Translations  []LangRef    `json:"translations"`              // languages that have this code; no text here
	WritingRefs   []WritingRef `json:"writing_refs,omitempty"`    // writings pages containing this code
}

// WritingRef links to a writings page where this code appears
type WritingRef struct {
	Type     string `json:"type"`      // writing type key (e.g. "tablets", "hidden-words")
	TypeName string `json:"type_name"` // display name
	Language string `json:"language"`  // language code
	LangName string `json:"lang_name"` // language display name
}

// PhelpsFile is written to assets/phelps/{base_pin}.json; groups all sub-codes.
type PhelpsFile struct {
	PIN      string    `json:"pin"`
	SubCodes []SubCode `json:"subcodes"`
}

// InventoryEntry for the concordance JSON served to the client
type InventoryEntry struct {
	PIN              string    `json:"pin"`
	Title            string    `json:"title,omitempty"`
	FirstLine        string    `json:"first_line"`
	FirstLineOrig    string    `json:"first_line_orig,omitempty"`
	Language         string    `json:"language,omitempty"`
	WordCount        string    `json:"word_count,omitempty"`
	Subjects         string    `json:"subjects,omitempty"`
	Notes            string    `json:"notes,omitempty"`
	Prefix           string    `json:"prefix"`
	TranslationCount int       `json:"translations,omitempty"` // number of translated versions
	Langs            []LangRef `json:"langs,omitempty"`        // language refs for translated versions
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
	staticPhelpsDir := filepath.Join(*outDir, "static", "data", "phelps")
	for _, dir := range []string{
		dataDir,
		filepath.Join(assetsDir, "prayers"),
		staticDir,
		staticPhelpsDir,
	} {
		must(os.MkdirAll(dir, 0755))
	}

	// 1. Languages
	log.Println("→ languages...")
	langs := queryLanguages()
	writeJSON(filepath.Join(dataDir, "languages.json"), langs)
	writeJSON(filepath.Join(staticDir, "languages.json"), langs)
	log.Printf("  %d languages", len(langs))

	// Build lang name lookup from ALL languages (not just prayer languages)
	langNames := map[string]string{}
	for _, l := range langs {
		langNames[l.Code] = l.Name
	}
	// Also load names for languages that only have writings (no prayers)
	allLangRows := doltQuery(`SELECT langcode, name FROM languages WHERE inlang='en'`)
	for _, row := range allLangRows[1:] {
		if len(row) >= 2 && langNames[row[0]] == "" {
			langNames[row[0]] = row[1]
		}
	}

	// 2a. First pass: one bulk query for all prayers across all languages
	log.Println("→ prayers (bulk query, all languages)...")
	allPrayers := queryAllPrayers() // langCode → prayers
	phelpsLangs := map[string][]LangRef{}  // phelps full code → deduped lang refs
	phelpsLangsSeen := map[string]map[string]bool{}
	for langCode, prayers := range allPrayers {
		for _, p := range prayers {
			if p.Phelps == "" {
				continue
			}
			if phelpsLangsSeen[p.Phelps] == nil {
				phelpsLangsSeen[p.Phelps] = map[string]bool{}
			}
			if phelpsLangsSeen[p.Phelps][langCode] {
				continue
			}
			phelpsLangsSeen[p.Phelps][langCode] = true
			phelpsLangs[p.Phelps] = append(phelpsLangs[p.Phelps], LangRef{
				Language: langCode,
				LangName: langNames[langCode],
			})
		}
		log.Printf("  %s: %d prayers", langCode, len(prayers))
	}

	// 2b. Second pass: write per-language JSON with "Also in:" translation lists + prayerbooks
	log.Println("→ loading all prayerbook category assignments (single query)...")
	allBookCats, allPrayerbooks, allBooks := queryAllBookCats(allPrayers)
	siblings := loadLanguageGroups()
	log.Printf("  loaded language-group siblings for %d languages", len(siblings))
	log.Printf("  book_cats loaded for %d languages, %d total prayerbooks", len(allBookCats), len(allBooks))
	writeJSON(filepath.Join(dataDir, "prayerbooks.json"), allBooks)
	// Also expose the index as a static file so client-side code (e.g. /p/?v=)
	// can fetch it without round-tripping through Hugo's site.Data.
	must(os.MkdirAll(staticDir, 0755))
	writeJSON(filepath.Join(staticDir, "prayerbooks.json"), allBooks)

	log.Println("→ prayers by language (pass 2: writing)...")
	for _, lang := range langs {
		prayers := allPrayers[lang.Code]
		bookCatsMap := allBookCats[lang.Code]
		prayerbooks := allPrayerbooks[lang.Code]

		for i, p := range prayers {
			if refs, ok := phelpsLangs[p.Phelps]; ok {
				others := make([]LangRef, 0, len(refs)-1)
				for _, r := range refs {
					if r.Language != lang.Code {
						others = append(others, r)
					}
				}
				prayers[i].Translations = others
			}
			if bc, ok := bookCatsMap[p.Phelps]; ok {
				prayers[i].BookCats = bc
			}
		}
		langFile := LangFile{
			Prayers:     prayers,
			Prayerbooks: prayerbooks,
			DefaultBook: pickDefaultBook(lang.Code, prayerbooks, prayers, siblings),
		}
		writeJSON(filepath.Join(assetsDir, "prayers", lang.Code+".json"), langFile)
		// Also write to static/ for client-side JS fetch (daily devotions page)
		must(os.MkdirAll(filepath.Join(staticDir, "prayers"), 0755))
		writeJSON(filepath.Join(staticDir, "prayers", lang.Code+".json"), langFile)
	}

	// Per-prayer permalink index: version UUID → [lang, phelps]
	// Used by /p/?v=<uuid> to look up which language file holds the rendering.
	log.Println("→ version index for /p/?v=<uuid> permalinks...")
	versionIndex := map[string][]string{}
	for langCode, prayers := range allPrayers {
		for _, p := range prayers {
			if p.Version != "" {
				versionIndex[p.Version] = []string{langCode, p.Phelps}
			}
			// Also index alternate sources' versions (same prayer text from
			// llm-translation, etc.) so permalinks to those resolve too.
			for _, alt := range p.AltSources {
				if alt.Version != "" {
					if _, exists := versionIndex[alt.Version]; !exists {
						versionIndex[alt.Version] = []string{langCode, p.Phelps}
					}
				}
			}
		}
	}
	log.Printf("  %d version UUIDs indexed", len(versionIndex))
	writeJSON(filepath.Join(staticDir, "version_index.json"), versionIndex)

	// Per-prayerbook view: /book/ overview + /book/?b=<code> shell uses
	// /data/book/<safe>.json which contains the prayers IN that book in
	// their actual languages (so multilingual books like mul-NA:bp render
	// hz/kj/diu/naq prayers together with language badges).
	log.Println("→ per-book JSON for /book/...")
	writePerBookJSON(staticDir, assetsDir, allBookCats, allPrayers, allBooks, phelpsLangs)

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
		langs := phelpsLangs[e.PIN]
		inventory[i].TranslationCount = len(langs)
		if len(langs) > 0 {
			inventory[i].Langs = langs
		}
		invMap[e.PIN] = inventory[i]
		base := basePINKey(e.PIN)
		invBasePINMap[base] = append(invBasePINMap[base], e.PIN)
	}
	for base := range invBasePINMap {
		sort.Strings(invBasePINMap[base])
	}
	// 4b. Add uncategorized codes (TMP, X-codes, UH/UHR) from writings table
	// These don't exist in the inventory table but should be searchable
	log.Println("→ uncategorized codes (TMP, X, UH)...")
	uncatCodes := queryUncategorized()
	invPINs := map[string]bool{}
	for _, e := range inventory {
		invPINs[e.PIN] = true
	}
	added := 0
	for _, e := range uncatCodes {
		if invPINs[e.PIN] {
			continue // already in inventory
		}
		langs := phelpsLangs[e.PIN]
		e.TranslationCount = len(langs)
		if len(langs) > 0 {
			e.Langs = langs
		}
		inventory = append(inventory, e)
		invMap[e.PIN] = e
		invPINs[e.PIN] = true
		added++
	}
	log.Printf("  %d uncategorized codes added to inventory", added)

	writeJSON(filepath.Join(staticDir, "inventory.json"), inventory)

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
		// Write to static/ for client-side JS rendering via detail.html
		writeJSON(filepath.Join(staticPhelpsDir, safe+".json"), pf)
	}

	// 6. Writings (non-prayer texts: hidden_words, aqdas, iqan, etc.)
	log.Println("→ writings...")
	writingRefs := generateWritings(assetsDir, dataDir, staticDir, langNames)
	log.Printf("  writing refs: %d base codes have backlinks", len(writingRefs))

	// 7. Enrich phelps files with writing backlinks
	if len(writingRefs) > 0 {
		log.Println("→ enriching phelps files with writing backlinks...")
		enriched := 0

		// Collect all base codes that have phelps files (from prayers + inventory)
		allBases := map[string]bool{}
		for base := range basePINMap {
			allBases[base] = true
		}
		for base := range invBasePINMap {
			allBases[base] = true
		}

		for base := range allBases {
			safe := strings.ToLower(base)
			path := filepath.Join(staticPhelpsDir, safe+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var pf PhelpsFile
			if err := json.Unmarshal(data, &pf); err != nil {
				continue
			}
			changed := false
			for i, sc := range pf.SubCodes {
				codeBase := writingBaseCode(sc.Code)
				if refs, ok := writingRefs[codeBase]; ok {
					pf.SubCodes[i].WritingRefs = refs
					changed = true
				}
			}
			if changed {
				writeJSON(path, pf)
				enriched++
			}
		}
		log.Printf("  enriched %d phelps files", enriched)

		// 8. Create phelps files for writing-only base codes that don't have one yet
		log.Println("→ creating phelps files for writing-only codes...")
		created := 0
		for base, refs := range writingRefs {
			safe := strings.ToLower(base)
			path := filepath.Join(staticPhelpsDir, safe+".json")
			if _, err := os.Stat(path); err == nil {
				continue // already exists
			}
			// Look up inventory metadata if available
			inv := invMap[base]
			pf := PhelpsFile{
				PIN: base,
				SubCodes: []SubCode{{
					Code:          base,
					Title:         inv.Title,
					FirstLine:     inv.FirstLine,
					FirstLineOrig: inv.FirstLineOrig,
					Language:      inv.Language,
					WordCount:     inv.WordCount,
					Subjects:      inv.Subjects,
					Notes:         inv.Notes,
					Translations:  []LangRef{},
					WritingRefs:   refs,
				}},
			}
			writeJSON(path, pf)
			writeJSON(filepath.Join(staticPhelpsDir, safe+".json"), pf)
			created++
		}
		log.Printf("  created %d writing-only phelps files", created)
	}

	// 8. Search index — full text excerpts for client-side search
	// Only one entry per phelps code per language (deduplicated)
	log.Println("→ search index...")
	type SearchEntry struct {
		Phelps   string `json:"p"`
		Language string `json:"l"`
		LangName string `json:"ln,omitempty"`
		Text     string `json:"t"` // first ~150 chars, stripped of HTML
		Category string `json:"c,omitempty"`
		Link     string `json:"u"` // URL to view this prayer
	}
	var searchEntries []SearchEntry
	searchSeen := map[string]bool{} // "phelps|lang" → already added
	for langCode, prayers := range allPrayers {
		for _, p := range prayers {
			if p.Phelps == "" || p.Source == "llm-translation" {
				continue
			}
			key := p.Phelps + "|" + langCode
			if searchSeen[key] {
				continue
			}
			searchSeen[key] = true
			text := stripHTML(p.Text)
			if len([]rune(text)) > 150 {
				text = string([]rune(text)[:150])
			}
			if text == "" {
				continue
			}
			cat := p.Category
			link := "/prayers/" + langCode + "/#" + p.Phelps
			searchEntries = append(searchEntries, SearchEntry{
				Phelps:   p.Phelps,
				Language: langCode,
				LangName: langNames[langCode],
				Text:     text,
				Category: cat,
				Link:     link,
			})
		}
	}
	// Also add writings — only first paragraph per base code per language
	writingRows := doltQuery(`
		SELECT w.phelps, w.language, LEFT(w.text, 400) as text, w.type
		FROM writings w
		INNER JOIN (
			SELECT MIN(phelps) as first_phelps, language, type
			FROM writings
			WHERE type IS NOT NULL AND type <> 'prayer'
			AND phelps IS NOT NULL AND phelps <> ''
			GROUP BY LEFT(phelps, 7), language, type
		) g ON w.phelps = g.first_phelps AND w.language = g.language AND w.type = g.type
		ORDER BY w.phelps, w.language
	`)
	writingTypeNames := map[string]string{
		"hidden_words": "Hidden Words", "aqdas": "Kitáb-i-Aqdas", "iqan": "Kitáb-i-Íqán",
		"gleanings": "Gleanings", "pm": "Prayers & Meditations", "saq": "Some Answered Questions",
		"tablets": "Tablets of Bahá'u'lláh", "days_remembrance": "Days of Remembrance",
		"ridvan": "Ridván Messages", "lawh": "Other Tablets",
	}
	writingTypeKeys := map[string]string{
		"hidden_words": "hidden-words", "aqdas": "aqdas", "iqan": "iqan",
		"gleanings": "gleanings", "pm": "pm", "saq": "saq",
		"tablets": "tablets", "days_remembrance": "days", "ridvan": "ridvan", "lawh": "lawh",
	}
	for _, row := range writingRows[1:] {
		if len(row) < 4 {
			continue
		}
		phelps, lang, text, wtype := row[0], row[1], row[2], row[3]
		text = stripHTML(text)
		if len([]rune(text)) > 300 {
			text = string([]rune(text)[:300])
		}
		if text == "" {
			continue
		}
		key := writingTypeKeys[wtype]
		if key == "" {
			continue
		}
		link := "/writings/" + key + "/" + lang + "/#" + phelps
		searchEntries = append(searchEntries, SearchEntry{
			Phelps:   phelps,
			Language: lang,
			LangName: langNames[lang],
			Text:     text,
			Category: writingTypeNames[wtype],
			Link:     link,
		})
	}
	writeJSON(filepath.Join(staticDir, "search.json"), searchEntries)
	log.Printf("  %d search entries", len(searchEntries))

	// 8. Generate prayer explorer: one entry per phelps code with translation count
	log.Println("→ generating prayer explorer (grouped by phelps, sorted by translation count)...")
	type ExplorerEntry struct {
		Phelps   string   `json:"p"`
		Count    int      `json:"n"`
		Langs    []string `json:"l"`
		First    string   `json:"f,omitempty"`
		Title    string   `json:"t,omitempty"`
		Subjects string   `json:"s,omitempty"`
	}
	explorerMap := map[string]*ExplorerEntry{}
	for _, e := range searchEntries {
		ee, ok := explorerMap[e.Phelps]
		if !ok {
			ee = &ExplorerEntry{Phelps: e.Phelps}
			explorerMap[e.Phelps] = ee
		}
		// Add language if not already present
		found := false
		for _, l := range ee.Langs {
			if l == e.Language { found = true; break }
		}
		if !found {
			ee.Langs = append(ee.Langs, e.Language)
			ee.Count = len(ee.Langs)
		}
	}
	// Also add prayer entries (some may not be in search index)
	for pin, langs := range phelpsLangs {
		ee, ok := explorerMap[pin]
		if !ok {
			ee = &ExplorerEntry{Phelps: pin}
			explorerMap[pin] = ee
		}
		for _, lr := range langs {
			found := false
			for _, l := range ee.Langs {
				if l == lr.Language { found = true; break }
			}
			if !found {
				ee.Langs = append(ee.Langs, lr.Language)
			}
		}
		ee.Count = len(ee.Langs)
	}
	// Enrich with inventory data
	for pin, ee := range explorerMap {
		if inv, ok := invMap[pin]; ok {
			ee.First = inv.FirstLine
			ee.Title = inv.Title
			ee.Subjects = inv.Subjects
		}
	}
	// Sort by count desc
	explorerList := make([]ExplorerEntry, 0, len(explorerMap))
	for _, ee := range explorerMap {
		sort.Strings(ee.Langs)
		explorerList = append(explorerList, *ee)
	}
	sort.Slice(explorerList, func(i, j int) bool {
		if explorerList[i].Count != explorerList[j].Count {
			return explorerList[i].Count > explorerList[j].Count
		}
		return explorerList[i].Phelps < explorerList[j].Phelps
	})
	writeJSON(filepath.Join(staticDir, "explorer.json"), explorerList)
	log.Printf("  %d explorer entries", len(explorerList))

	log.Println("Done!")
}

// BookPrayer is one prayer entry inside a per-book JSON file.
type BookPrayer struct {
	Phelps       string             `json:"phelps"`
	Lang         string             `json:"lang"`
	LangName     string             `json:"lang_name,omitempty"`
	Name         string             `json:"name,omitempty"`
	Text         string             `json:"text"`
	Category     string             `json:"category,omitempty"`
	CatOrder     int                `json:"cat_order,omitempty"`
	OrderInCat   int                `json:"order_in_cat,omitempty"`
	Version      string             `json:"version,omitempty"`
	VersionB36   string             `json:"v,omitempty"`
	BookCats     map[string]BookCat `json:"book_cats,omitempty"`    // prayerbook code → category assignment
	Translations []LangRef          `json:"translations,omitempty"` // other languages with this phelps code
}

// BookFile is the top-level structure written to /data/book/<safe>.json.
type BookFile struct {
	Code    string         `json:"code"`
	Name    string         `json:"name"`
	Prayers []BookPrayer   `json:"prayers"`
	LangSet map[string]int `json:"lang_counts"`
}

// BookOverview is one entry in /data/books.json (the /book/ index page).
type BookOverview struct {
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	PrayerCount int            `json:"count"`
	LangCounts  map[string]int `json:"langs"`
	Categories  []string       `json:"categories"`
}

// safeBookCode replaces characters that are awkward in filenames or URLs and
// lowercases the result so it matches Hugo's lowercased page paths.
// `mul-NA:bp` → `mul-na-bp`.
func safeBookCode(code string) string {
	return strings.ToLower(strings.ReplaceAll(code, ":", "-"))
}

// writePerBookJSON emits /data/book/<safe>.json for every prayerbook plus
// /data/books.json index. Each per-book file lists prayers in their actual
// languages so multilingual compilations (mul-NA:bp etc.) render with mixed
// content; single-language books look the same as the per-language pages
// they were extracted from but are addressable as a book entity.
//
// Source of truth is a direct join PBS↔writings via source_id, since that's
// what defines membership of a writings row in a book. Going through the
// allBookCats map would over-attribute (it tracks phelps→book without
// remembering which writings.language each PBS row was for).
func writePerBookJSON(staticDir, assetsDir string,
	allBookCats map[string]map[string]map[string]BookCat,
	allPrayers map[string][]Prayer,
	allBooks []BookRef,
	phelpsLangs map[string][]LangRef,
) {
	must(os.MkdirAll(filepath.Join(staticDir, "prayers"), 0755))
	must(os.MkdirAll(filepath.Join(assetsDir, "prayers"), 0755))

	langName := map[string]string{}
	for _, l := range loadLanguagesForBooks() {
		langName[l.Code] = l.Name
	}

	byLangPhelps := map[string]map[string]Prayer{}
	for lang, prayers := range allPrayers {
		m := make(map[string]Prayer, len(prayers))
		for _, p := range prayers {
			m[p.Phelps] = p
		}
		byLangPhelps[lang] = m
	}

	type entry struct {
		lang, phelps, cat string
		catOrd, ordInCat  int
	}
	bookEntries := map[string][]entry{}
	// JOIN on phelps + language (derived from source_language prefix) instead of
	// source_id+source. The old form excluded bpapp-only writings since those
	// have source='bahaiprayers.app' and a different source_id space.
	rows := doltQuery(`
		SELECT pbs.source_language, w.language, pbs.phelps_code,
		       COALESCE(pbs.category_name,''),
		       COALESCE(pbs.category_order,0),
		       COALESCE(pbs.order_in_category,0)
		FROM prayer_book_structure pbs
		JOIN writings w
		    ON w.phelps = pbs.phelps_code
		   AND w.language = SUBSTRING_INDEX(pbs.source_language, ':', 1)
		WHERE pbs.phelps_code IS NOT NULL AND pbs.phelps_code <> ''
	`)
	for _, r := range rows[1:] {
		if len(r) < 6 {
			continue
		}
		catOrd, ordInCat := 0, 0
		fmt.Sscanf(r[4], "%d", &catOrd)
		fmt.Sscanf(r[5], "%d", &ordInCat)
		bookEntries[r[0]] = append(bookEntries[r[0]], entry{
			lang: r[1], phelps: r[2], cat: r[3],
			catOrd: catOrd, ordInCat: ordInCat,
		})
	}

	overviews := make([]BookOverview, 0, len(allBooks))
	for _, b := range allBooks {
		entries := bookEntries[b.Code]
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].catOrd != entries[j].catOrd {
				return entries[i].catOrd < entries[j].catOrd
			}
			if entries[i].ordInCat != entries[j].ordInCat {
				return entries[i].ordInCat < entries[j].ordInCat
			}
			return entries[i].phelps < entries[j].phelps
		})

		bp := make([]BookPrayer, 0, len(entries))
		langCounts := map[string]int{}
		catSet := map[string]bool{}
		for _, e := range entries {
			pmap := byLangPhelps[e.lang]
			if pmap == nil {
				continue
			}
			p, ok := pmap[e.phelps]
			if !ok {
				continue
			}
			// translations: other languages that have this phelps code
			var trans []LangRef
			if refs, ok := phelpsLangs[e.phelps]; ok {
				trans = make([]LangRef, 0, len(refs))
				for _, r := range refs {
					if r.Language != e.lang {
						trans = append(trans, r)
					}
				}
			}
			// book_cats: prayerbook category assignments for this phelps in this language
			var bcats map[string]BookCat
			if langMap := allBookCats[e.lang]; langMap != nil {
				if bc, ok := langMap[e.phelps]; ok {
					bcats = bc
				}
			}
			bp = append(bp, BookPrayer{
				Phelps: e.phelps, Lang: e.lang, LangName: langName[e.lang],
				Name: p.Name, Text: p.Text, Category: e.cat,
				CatOrder: e.catOrd, OrderInCat: e.ordInCat,
				Version: p.Version, VersionB36: p.VersionB36,
				BookCats: bcats, Translations: trans,
			})
			langCounts[e.lang]++
			if e.cat != "" {
				catSet[e.cat] = true
			}
		}
		bf := BookFile{Code: b.Code, Name: b.Name, Prayers: bp, LangSet: langCounts}
		// Books live in the same namespace as language pages at /prayers/<safe>/.
		// safeBookCode does ':' → '-' and lowercases (matches Hugo URL paths).
		// assets/ copy is for Hugo's resources.Get during page generation;
		// static/ copy is for client-side fetch.
		safe := safeBookCode(b.Code)
		writeJSON(filepath.Join(staticDir, "prayers", safe+".json"), bf)
		writeJSON(filepath.Join(assetsDir, "prayers", safe+".json"), bf)

		cats := make([]string, 0, len(catSet))
		for c := range catSet {
			cats = append(cats, c)
		}
		sort.Strings(cats)
		overviews = append(overviews, BookOverview{
			Code: b.Code, Name: b.Name, PrayerCount: len(bp),
			LangCounts: langCounts, Categories: cats,
		})
	}
	sort.Slice(overviews, func(i, j int) bool { return overviews[i].Name < overviews[j].Name })
	_ = overviews // BookOverview is no longer surfaced; books and languages share /prayers/
	log.Printf("  wrote %d per-book JSON files", len(overviews))
}

// loadLanguagesForBooks pulls langcode→name (English) from the languages
// table — used by writePerBookJSON to label per-prayer language badges.
// Named distinctly from the unrelated `Language` consumers in main().
func loadLanguagesForBooks() []Language {
	rows := doltQuery(`
		SELECT langcode, name FROM languages WHERE inlang = 'en'
		ORDER BY langcode
	`)
	out := make([]Language, 0, len(rows)-1)
	for _, r := range rows[1:] {
		if len(r) < 2 {
			continue
		}
		out = append(out, Language{Code: r[0], Name: r[1]})
	}
	return out
}

// WritingType metadata for the writings index
type WritingType struct {
	Key        string        `json:"key"`
	Title      string        `json:"title"`
	Author     string        `json:"author"`
	ShowNames  bool          `json:"show_names,omitempty"`
	SingleBook bool          `json:"single_book,omitempty"`
	Langs      []WritingLang `json:"langs"`
}

type WritingLang struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Count int    `json:"count"`
	RTL   bool   `json:"rtl,omitempty"`
}

// WritingEntry is one paragraph/verse/section in a writing
type WritingEntry struct {
	Phelps string `json:"phelps"`
	Name   string `json:"name,omitempty"`
	Text   string `json:"text"`
	Order  int    `json:"order"`           // numeric position for sorting / range-selection
	Label  string `json:"label,omitempty"` // display label; differs from Order for chapter:para works (Íqán: "1:1", "2:186")
}

// WritingBook groups entries under a book/tablet heading
type WritingBook struct {
	Base    string         `json:"base"`              // base Phelps code (e.g. BH02324)
	Title   string         `json:"title"`             // book/tablet title from first entry's name
	Entries []WritingEntry `json:"entries"`
}

// WritingLangFile is written to assets/writings/{type}/{lang}.json
type WritingLangFile struct {
	Books []WritingBook `json:"books"`
}

var writingTypes = []struct {
	Key        string
	Title      string
	Author     string
	DBType     string
	SingleBook bool // treat all entries as one book (don't group by base code)
	ShowNames  bool // show entry names in the UI (useful for SAQ titles, Gleanings Roman numerals)
	SplitParas bool // split text on \n\n into individual paragraph entries
}{
	{"hidden-words", "The Hidden Words", "Bahá'u'lláh", "hidden_words", false, false, false},
	{"aqdas", "Kitáb-i-Aqdas", "Bahá'u'lláh", "aqdas", true, false, false},
	{"iqan", "Kitáb-i-Íqán", "Bahá'u'lláh", "iqan", true, false, false},
	{"gleanings", "Gleanings", "Bahá'u'lláh", "gleanings", true, true, false},
	{"pm", "Prayers & Meditations", "Bahá'u'lláh", "pm", true, false, false},
	{"saq", "Some Answered Questions", "'Abdu'l-Bahá", "saq", true, true, false},
	{"tablets", "Tablets of Bahá'u'lláh", "Bahá'u'lláh", "tablets", false, false, false},
	{"days", "Days of Remembrance", "Bahá'u'lláh", "days_remembrance", true, true, false},
	{"ridvan", "Ridván Messages", "Universal House of Justice", "ridvan", true, true, false},
	{"divineplan", "Tablets of the Divine Plan", "'Abdu'l-Bahá", "divineplan", false, false, false},
	{"lawh", "Other Tablets", "Bahá'u'lláh", "lawh", false, false, false},
	{"gpb", "God Passes By", "Shoghi Effendi", "book", false, false, true},
}

// generateWritings returns a reverse index: base phelps code → []WritingRef
func generateWritings(assetsDir, dataDir, staticDir string, langNames map[string]string) map[string][]WritingRef {
	// Query all writing entries grouped by type and language
	rows := doltQuery(`
		SELECT type, language, phelps, COALESCE(name,''), text, COALESCE(source_id,'')
		FROM writings
		WHERE type IS NOT NULL AND type <> 'prayer'
		  AND phelps IS NOT NULL AND phelps <> ''
		ORDER BY type, language, CAST(REGEXP_REPLACE(source_id, '[^0-9]', '') AS UNSIGNED), phelps
	`)

	// Group: dbType → lang → []WritingEntry
	typeData := map[string]map[string][]WritingEntry{}
	for _, row := range rows[1:] {
		if len(row) < 6 {
			continue
		}
		dbType, lang, phelps, name, text := row[0], row[1], row[2], row[3], row[4]
		_ = row[5] // source_id used for ORDER BY only
		if typeData[dbType] == nil {
			typeData[dbType] = map[string][]WritingEntry{}
		}
		fallbackOrder := len(typeData[dbType][lang]) + 1
		typeData[dbType][lang] = append(typeData[dbType][lang], WritingEntry{
			Phelps: phelps,
			Name:   name,
			Text:   text,
			Order:  writingEntryNumber(phelps, fallbackOrder),
			Label:  writingEntryLabel(phelps),
		})
	}

	var writingsIndex []WritingType
	for _, wt := range writingTypes {
		langData := typeData[wt.DBType]
		if len(langData) == 0 {
			continue
		}

		// Create output directory
		outDir := filepath.Join(assetsDir, "writings", wt.Key)
		must(os.MkdirAll(outDir, 0755))

		var wlangs []WritingLang
		for lang, entries := range langData {
			name := langNames[lang]
			if name == "" {
				name = lang
			}

			// Group entries into books
			var books []WritingBook
			if wt.SingleBook {
				// All entries in one book
				books = []WritingBook{{
					Base:    "",
					Title:   wt.Title,
					Entries: entries,
				}}
			} else {
				// Fixed, localized book titles for bases where the first
				// entry's name doesn't yield the right title under the
				// strip-digits heuristic (e.g. HW preamble entry is named
				// "…— Preamble"). Missing translations fall back to English.
				// TODO: lift to i18n/*.yaml once we agree on the key naming.
				localizedTitles := map[string]map[string]string{
					"BH00386": {
						"en": "Arabic Hidden Words",
						"ar": "الكلمات المكنونة العربية",
						"fa": "کلمات مکنونه عربی",
						"de": "Die Arabischen Verborgenen Worte",
						"fr": "Les Paroles Cachées en arabe",
						"es": "Las Palabras Ocultas en árabe",
						"it": "Le Parole Celate in arabo",
						"pt": "As Palavras Ocultas em árabe",
						"nl": "De Arabische Verborgen Woorden",
						"ru": "Сокровенные Слова (арабские)",
						"zh-Hans": "隐言经(阿拉伯文)",
						"zh-Hant": "隱言經(阿拉伯文)",
						"ja":      "アラビア語の隠されし言葉",
						"ko":      "아랍어 감추어진 말씀",
						"tr":      "Arapça Gizli Sözler",
						"pl":      "Słowa Ukryte (arabskie)",
						"sv":      "De Fördolda Orden (arabiska)",
						"hu":      "Arab Rejtett Szavak",
						"fi":      "Kätketyt sanat (arabia)",
						"el":      "Αραβικές Κρυμμένες Λέξεις",
						"ro":      "Cuvintele Ascunse (arabă)",
						"eo":      "Kaŝitaj Vortoj (araba)",
					},
					"BH00113": {
						"en": "Persian Hidden Words",
						"ar": "الكلمات المكنونة الفارسية",
						"fa": "کلمات مکنونه فارسی",
						"de": "Die Persischen Verborgenen Worte",
						"fr": "Les Paroles Cachées en persan",
						"es": "Las Palabras Ocultas en persa",
						"it": "Le Parole Celate in persiano",
						"pt": "As Palavras Ocultas em persa",
						"nl": "De Perzische Verborgen Woorden",
						"ru": "Сокровенные Слова (персидские)",
						"zh-Hans": "隐言经(波斯文)",
						"zh-Hant": "隱言經(波斯文)",
						"ja":      "ペルシア語の隠されし言葉",
						"ko":      "페르시아어 감추어진 말씀",
						"tr":      "Farsça Gizli Sözler",
						"pl":      "Słowa Ukryte (perskie)",
						"sv":      "De Fördolda Orden (persiska)",
						"hu":      "Perzsa Rejtett Szavak",
						"fi":      "Kätketyt sanat (persia)",
						"el":      "Περσικές Κρυμμένες Λέξεις",
						"ro":      "Cuvintele Ascunse (persană)",
						"eo":      "Kaŝitaj Vortoj (persa)",
					},
				}
				fixedTitle := func(base, lng string) string {
					if byLang, ok := localizedTitles[base]; ok {
						if t, ok := byLang[lng]; ok {
							return t
						}
						if t, ok := byLang["en"]; ok {
							return t
						}
					}
					return ""
				}
				// Group by base Phelps code
				bookMap := map[string]*WritingBook{}
				var bookOrder []string
				for _, e := range entries {
					base := writingBaseCode(e.Phelps)
					if bk, ok := bookMap[base]; ok {
						bk.Entries = append(bk.Entries, e)
					} else {
						var title string
						if ft := fixedTitle(base, lang); ft != "" {
							title = ft
						} else {
							// Strip trailing section marker to get book title
							// "Persian Hidden Word 1" → "Persian Hidden Words"
							// "Epistle to the Son of the Wolf §1" → "Epistle to the Son of the Wolf"
							// "Lawḥ-i-Karmil (Tablet of Carmel)" → unchanged
							title = strings.TrimRight(e.Name, " 0123456789§¶")
							if title != e.Name && strings.HasSuffix(title, "Word") {
								title += "s" // pluralize: "Persian Hidden Word" → "Persian Hidden Words"
							}
						}
						bookMap[base] = &WritingBook{
							Base:    base,
							Title:   title,
							Entries: []WritingEntry{e},
						}
						bookOrder = append(bookOrder, base)
					}
				}
				for _, base := range bookOrder {
					books = append(books, *bookMap[base])
				}
			}

			// Split paragraphs if requested (e.g. GPB: each chapter → book, each paragraph → entry)
			paraCount := 0
			if wt.SplitParas {
				for i := range books {
					var split []WritingEntry
					for _, e := range books[i].Entries {
						paras := strings.Split(e.Text, "\n\n")
						for _, p := range paras {
							p = strings.TrimSpace(p)
							if p == "" {
								continue
							}
							split = append(split, WritingEntry{
								Phelps: e.Phelps,
								Name:   "",
								Text:   "<p>" + p + "</p>",
								Order:  len(split) + 1,
							})
						}
					}
					books[i].Entries = split
					paraCount += len(split)
				}
			}

			entryCount := len(entries)
			if wt.SplitParas {
				entryCount = paraCount
			}
			wlangs = append(wlangs, WritingLang{
				Code:  lang,
				Name:  name,
				Count: entryCount,
				RTL:   rtlLangs[lang],
			})
			wlf := WritingLangFile{Books: books}
			writeJSON(filepath.Join(outDir, lang+".json"), wlf)
			// Also write to static/ for client-side JS fetch (daily devotions page)
			staticWritDir := filepath.Join(staticDir, "writings", wt.Key)
			must(os.MkdirAll(staticWritDir, 0755))
			writeJSON(filepath.Join(staticWritDir, lang+".json"), wlf)
		}
		sort.Slice(wlangs, func(i, j int) bool { return wlangs[i].Name < wlangs[j].Name })

		writingsIndex = append(writingsIndex, WritingType{
			Key:        wt.Key,
			Title:      wt.Title,
			Author:     wt.Author,
			ShowNames:  wt.ShowNames,
			SingleBook: wt.SingleBook,
			Langs:      wlangs,
		})
		log.Printf("  %s: %d languages", wt.Key, len(wlangs))
	}

	writeJSON(filepath.Join(dataDir, "writings.json"), writingsIndex)
	writeJSON(filepath.Join(staticDir, "writings.json"), writingsIndex)
	log.Printf("  %d writing types total", len(writingsIndex))

	// Build reverse index: base phelps code → writing refs (deduped by type+lang)
	reverseIdx := map[string][]WritingRef{}
	for _, wt := range writingTypes {
		langData := typeData[wt.DBType]
		for lang, entries := range langData {
			// Collect unique base codes in this lang
			bases := map[string]bool{}
			for _, e := range entries {
				bases[writingBaseCode(e.Phelps)] = true
			}
			langName := langNames[lang]
			if langName == "" {
				langName = lang
			}
			ref := WritingRef{
				Type:     wt.Key,
				TypeName: wt.Title,
				Language: lang,
				LangName: langName,
			}
			for base := range bases {
				reverseIdx[base] = append(reverseIdx[base], ref)
			}
		}
	}
	return reverseIdx
}

// writingBaseCode extracts the book-level code from a writing phelps code.
// BH02324005 → BH02324, BH00386A01 → BH00386, BH00001042 → BH00001,
// BH00001G037 → BH00001, BH00113P01 → BH00113, UHR2024 → UHR2024
// Strategy: the base is always the first 7 chars IF char 8+ is a non-letter
// or a single letter followed by digits.
// writingEntryNumber returns the canonical entry number from the last three
// characters of a phelps code, which encodes the verse / paragraph / section
// number for multi-entry works:
//   - HW Arabic   BH00386A71 → "A71" → 71  (and A00 → 0 for the preamble)
//   - HW Persian  BH00113P83 → "P83" → 83  (P00 preamble, P83 conclusion)
//   - Aqdas       BH00001190 → "190" → 190
//   - Íqán        BH000022186 → "186" (paragraph within chapter)
//   - Gleanings   BH00001G166 → "G166" → 166
// Standalone 7-char prayer codes have no suffix and fall back to sequential
// position.
// writingEntryLabel returns a human-readable display label that may differ
// from the numeric Order. Currently used for Íqán to show "chapter:paragraph"
// (BH000021001 → "1:1"), otherwise returns empty so the UI falls back to Order.
func writingEntryLabel(pin string) string {
	// Íqán: BH00002 base, 11-char total, digit-only suffix of length 4 where
	// the first digit is the chapter and the rest is the paragraph.
	if len(pin) == 11 && strings.HasPrefix(pin, "BH00002") {
		suffix := pin[7:]
		allDigits := true
		for _, c := range suffix {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			chapter := suffix[:1]
			para := strings.TrimLeft(suffix[1:], "0")
			if para == "" {
				para = "0"
			}
			return chapter + ":" + para
		}
	}
	return ""
}

func writingEntryNumber(pin string, fallback int) int {
	if len(pin) < 10 {
		return fallback
	}
	// Íqán uses 11-char chapter-prefixed codes that duplicate paragraph
	// numbers across chapters. Keep sequential Order; Label carries "ch:para".
	if len(pin) > 10 && pin[7] >= '0' && pin[7] <= '9' {
		return fallback
	}
	suffix := pin[len(pin)-3:]
	// Strip a single leading uppercase letter (A/P for HW, G for Gleanings, …)
	if suffix[0] >= 'A' && suffix[0] <= 'Z' {
		suffix = suffix[1:]
	}
	if suffix == "" {
		return fallback
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return fallback
		}
	}
	n, err := strconv.Atoi(suffix)
	if err != nil {
		return fallback
	}
	return n
}

func writingBaseCode(pin string) string {
	if len(pin) <= 7 {
		return pin
	}
	// Everything after position 7 is the suffix
	// If it starts with a letter (A, P, G, K, S, etc.) followed by digits → strip all
	// If it's all digits → strip all
	// This covers: A01 (HW Arabic), P01 (HW Persian), G037 (Gleanings), 001 (Aqdas/Iqan/Tablets)
	suffix := pin[7:]
	if len(suffix) > 0 {
		first := suffix[0]
		if first >= '0' && first <= '9' {
			// Numeric suffix → base is first 7
			return pin[:7]
		}
		if first >= 'A' && first <= 'Z' && len(suffix) > 1 {
			// Alpha prefix + rest → check if rest is digits
			rest := suffix[1:]
			allDigits := true
			for _, c := range rest {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return pin[:7]
			}
		}
	}
	return pin
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
		    AND (w.type IS NULL OR w.type = 'prayer')
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

// queryAllPrayers fetches every prayer for every language in one SQL query
// and groups them by language, then by phelps within each language.
func queryAllPrayers() map[string][]Prayer {
	rows := doltQuery(`
		SELECT w.language, w.phelps, w.text, COALESCE(w.name,''), w.source, w.version, COALESCE(w.notes,''),
		       COALESCE(pbs.category_name,''),
		       COALESCE(pbs.category_order,0),
		       COALESCE(pbs.order_in_category,0),
		       COALESCE(pbs.source_language,'')
		FROM writings w
		LEFT JOIN prayer_book_structure pbs
		    ON pbs.source_id = w.source_id
		    AND pbs.phelps_code = w.phelps
		WHERE w.phelps IS NOT NULL AND w.phelps <> ''
		  AND (w.type IS NULL OR w.type = 'prayer')
		ORDER BY w.language,
		         CASE w.source WHEN 'bahaiprayers.net' THEN 0 ELSE 1 END,
		         COALESCE(pbs.category_order,9999),
		         COALESCE(pbs.order_in_category,9999),
		         w.phelps
	`)

	type rawRow struct {
		lang, phelps, text, name, source, version, notes string
		catName                                           string
		catOrd, ordInCat                                  int
		book                                              string
	}
	type group struct {
		primary rawRow
		alts    []PrayerSource
	}

	langGroups := map[string]map[string]*group{}
	langOrder := map[string][]string{}

	for _, row := range rows[1:] {
		if len(row) < 11 {
			continue
		}
		catOrd, ordInCat := 0, 0
		fmt.Sscanf(row[8], "%d", &catOrd)
		fmt.Sscanf(row[9], "%d", &ordInCat)
		r := rawRow{
			lang: row[0], phelps: row[1], text: row[2], name: row[3],
			source: row[4], version: row[5], notes: row[6],
			catName: row[7], catOrd: catOrd, ordInCat: ordInCat,
			book: row[10],
		}
		if langGroups[r.lang] == nil {
			langGroups[r.lang] = map[string]*group{}
		}
		if g, ok := langGroups[r.lang][r.phelps]; !ok {
			langGroups[r.lang][r.phelps] = &group{primary: r}
			langOrder[r.lang] = append(langOrder[r.lang], r.phelps)
		} else if r.source == "bahaiprayers.net" && g.primary.source != "bahaiprayers.net" {
			g.alts = append([]PrayerSource{{
				Source: g.primary.source, Version: g.primary.version,
				Text: g.primary.text, Notes: g.primary.notes,
			}}, g.alts...)
			g.primary = r
		} else if r.source != g.primary.source {
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

	result := map[string][]Prayer{}
	for lang, order := range langOrder {
		prayers := make([]Prayer, 0, len(order))
		for _, phelps := range order {
			g := langGroups[lang][phelps]
			p := Prayer{
				Phelps:        phelps,
				Text:          g.primary.text,
				Name:          g.primary.name,
				Category:      g.primary.catName,
				CategoryOrder: g.primary.catOrd,
				OrderInCat:    g.primary.ordInCat,
				Source:        g.primary.source,
				Version:       g.primary.version,
				VersionB36:    uuidToBase36(g.primary.version),
				Notes:         g.primary.notes,
				Book:          g.primary.book,
			}
			if len(g.alts) > 0 {
				p.AltSources = g.alts
			}
			prayers = append(prayers, p)
		}
		result[lang] = prayers
	}
	return result
}

// prayerBookEntry holds one row from prayer_book_structure
type prayerBookEntry struct {
	bookCode   string
	bookName   string
	catName    string
	catOrder   int
	ordInCat   int
}

// queryAllBookCats loads the full prayer_book_structure table (~10K rows) plus
// prayerbook language names into memory, then builds per-language index maps in Go.
// Returns:
//   - pbIndex: phelps_code → []prayerBookEntry (all prayerbooks containing this code)
//   - bookNames: bookCode → display name
func loadPrayerBookStructure() (
	pbIndex map[string][]prayerBookEntry,
	bookNames map[string]string,
) {
	rows := doltQuery(`
		SELECT pbs.phelps_code, pbs.source_language, l.name,
		       pbs.category_name, pbs.category_order, pbs.order_in_category
		FROM prayer_book_structure pbs
		JOIN languages l ON l.langcode = pbs.source_language AND l.inlang = 'en'
		ORDER BY pbs.source_language, pbs.category_order, pbs.order_in_category
	`)

	pbIndex = map[string][]prayerBookEntry{}
	bookNames = map[string]string{}

	for _, row := range rows[1:] {
		if len(row) < 6 {
			continue
		}
		phelps, bookCode, bookName, catName := row[0], row[1], row[2], row[3]
		catOrd, ordInCat := 0, 0
		fmt.Sscanf(row[4], "%d", &catOrd)
		fmt.Sscanf(row[5], "%d", &ordInCat)
		pbIndex[phelps] = append(pbIndex[phelps], prayerBookEntry{
			bookCode: bookCode, bookName: bookName,
			catName: catName, catOrder: catOrd, ordInCat: ordInCat,
		})
		bookNames[bookCode] = bookName
	}
	return pbIndex, bookNames
}

// buildLangBookCats takes the in-memory pbIndex and this language's prayers,
// and returns:
//   - bookCats: phelps → bookCode → BookCat (every book that contains
//     any of this lang's phelps codes — used by the JS to show "in book X,
//     this prayer is in category Y")
//   - books: the picker list — every book that contains at least one of
//     this lang's phelps codes, sorted by name. The JS at runtime hides
//     books whose coverage of THIS PAGE's prayers is too low (filtering
//     here would have to repeat per-page logic the runtime already needs
//     for native-vs-fallback decisions; the original-language vs
//     coverage info is already in book_cats).
func buildLangBookCats(pbIndex map[string][]prayerBookEntry, prayers []Prayer) (
	bookCats map[string]map[string]BookCat,
	books []BookRef,
) {
	bookCats = map[string]map[string]BookCat{}
	bookNameMap := map[string]string{}

	for _, p := range prayers {
		if p.Phelps == "" {
			continue
		}
		entries, ok := pbIndex[p.Phelps]
		if !ok {
			continue
		}
		m := map[string]BookCat{}
		for _, e := range entries {
			if _, exists := m[e.bookCode]; !exists {
				m[e.bookCode] = BookCat{Category: e.catName, CatOrder: e.catOrder, OrderInCat: e.ordInCat}
			}
			bookNameMap[e.bookCode] = e.bookName
		}
		if len(m) > 0 {
			bookCats[p.Phelps] = m
		}
	}

	// Count entries per book from the PBS index. Each entry in pbIndex is
	// one (book, phelps) row, so this matches what /prayers/<book>/ shows.
	bookCounts := map[string]int{}
	for _, entries := range pbIndex {
		for _, e := range entries {
			bookCounts[e.bookCode]++
		}
	}
	for code, name := range bookNameMap {
		books = append(books, BookRef{Code: code, Name: name, Count: bookCounts[code]})
	}
	sort.Slice(books, func(i, j int) bool {
		return books[i].Name < books[j].Name
	})
	return bookCats, books
}

// loadLanguageGroups returns lang -> list of sibling lang codes from the
// language_groups table (Belgium, Pacific Oceania, etc.). Used as a
// closeness fallback when picking a default book — if a language has no
// own :bp book and isn't in any multilingual book, we'd rather show a
// linguistic neighbor's book than English.
func loadLanguageGroups() map[string][]string {
	rows := doltQuery(`
		SELECT m1.language, m2.language
		FROM language_group_members m1
		JOIN language_group_members m2 ON m2.group_id = m1.group_id AND m2.language <> m1.language
		ORDER BY m1.language, m2.display_order
	`)
	out := map[string][]string{}
	seen := map[string]map[string]bool{}
	for _, row := range rows[1:] {
		if len(row) < 2 {
			continue
		}
		lang, sibling := row[0], row[1]
		if seen[lang] == nil {
			seen[lang] = map[string]bool{}
		}
		if seen[lang][sibling] {
			continue
		}
		seen[lang][sibling] = true
		out[lang] = append(out[lang], sibling)
	}
	return out
}

// pickDefaultBook resolves the prayerbook to select on first load for `lang`.
// Fallback chain:
//   1. own-language :bp (e.g. "eo:bpnet" for Esperanto)
//   2. the most common book among the language's own prayers — this picks
//      mul-NA:bp for hz/kj/diu/naq, nai-CA:bp for First Nations languages,
//      etc., based on actual data rather than a hard-coded map
//   3. linguistically-near sibling's :bp via language_groups (e.g. tpi → fj:bp)
//   4. en:bpnet (universal fallback)
//   5. first available book in the picker
//   6. "" (no book; caller may hide the picker)
func pickDefaultBook(lang string, books []BookRef, prayers []Prayer, siblings map[string][]string) string {
	wantOwn := lang + ":bpnet"
	for _, b := range books {
		if b.Code == wantOwn {
			return wantOwn
		}
	}
	bookCounts := map[string]int{}
	for _, p := range prayers {
		if p.Book != "" {
			bookCounts[p.Book]++
		}
	}
	if len(bookCounts) > 0 {
		bestCode, bestCount := "", 0
		for code, cnt := range bookCounts {
			if cnt > bestCount || (cnt == bestCount && code < bestCode) {
				bestCode, bestCount = code, cnt
			}
		}
		if bestCode != "" {
			return bestCode
		}
	}
	// Linguistic neighbors: if this lang is in a language_group, try the
	// siblings' :bp books in display order before falling all the way to en.
	bookSet := map[string]bool{}
	for _, b := range books {
		bookSet[b.Code] = true
	}
	for _, sibling := range siblings[lang] {
		want := sibling + ":bpnet"
		if bookSet[want] {
			return want
		}
	}
	for _, b := range books {
		if b.Code == "en:bpnet" {
			return "en:bpnet"
		}
	}
	if len(books) > 0 {
		return books[0].Code
	}
	return ""
}

// queryAllBookCats is the public entry point: loads PBS once, then builds
// per-language maps using the already-collected allPrayers data.
// Also returns the global sorted list of all prayerbooks.
func queryAllBookCats(allPrayers map[string][]Prayer) (
	langBookCats map[string]map[string]map[string]BookCat,
	langBooks map[string][]BookRef,
	globalBooks []BookRef,
) {
	pbIndex, bookNames := loadPrayerBookStructure()

	// Count entries per book — one PBS row = one (book, phelps) entry.
	bookCounts := map[string]int{}
	for _, entries := range pbIndex {
		for _, e := range entries {
			bookCounts[e.bookCode]++
		}
	}

	// Build sorted global prayerbook list
	allCodes := make([]string, 0, len(bookNames))
	for code := range bookNames {
		allCodes = append(allCodes, code)
	}
	sort.Slice(allCodes, func(i, j int) bool {
		return bookNames[allCodes[i]] < bookNames[allCodes[j]]
	})
	globalBooks = make([]BookRef, 0, len(allCodes))
	for _, code := range allCodes {
		globalBooks = append(globalBooks, BookRef{Code: code, Name: bookNames[code], Count: bookCounts[code]})
	}

	langBookCats = map[string]map[string]map[string]BookCat{}
	langBooks = map[string][]BookRef{}

	for lang, prayers := range allPrayers {
		bc, bks := buildLangBookCats(pbIndex, prayers)
		if len(bc) > 0 {
			langBookCats[lang] = bc
		}
		// Always ensure the English prayerbook is available as an option,
		// even if none of this language's prayers have an English entry yet —
		// it's the universal cross-reference book.
		if enName, ok := bookNames["en:bpnet"]; ok {
			hasEn := false
			for _, b := range bks {
				if b.Code == "en:bpnet" {
					hasEn = true
					break
				}
			}
			if !hasEn {
				bks = append(bks, BookRef{Code: "en:bpnet", Name: enName})
				sort.Slice(bks, func(i, j int) bool { return bks[i].Name < bks[j].Name })
			}
		}
		if len(bks) > 0 {
			langBooks[lang] = bks
		}
	}
	return langBookCats, langBooks, globalBooks
}

func queryInventory() []InventoryEntry {
	rows := doltQuery(`SELECT PIN,
		COALESCE(Title,''),
		COALESCE(` + "`First line (translated)`" + `,''),
		COALESCE(` + "`First line (original)`" + `,''),
		COALESCE(CAST(Language AS CHAR),''),
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

// queryUncategorized returns inventory entries for codes not in the inventory table:
// TMP (unresolved), X-codes (XAB, XBH, XBB), UH/UHR (Universal House of Justice)
func queryUncategorized() []InventoryEntry {
	rows := doltQuery(`
		SELECT phelps, COALESCE(name,''), COALESCE(LEFT(text,120),''),
		       COALESCE(notes,''), COUNT(DISTINCT language) as lang_count
		FROM writings
		WHERE phelps IS NOT NULL AND phelps <> ''
		  AND (phelps LIKE 'TMP%' OR phelps LIKE 'X%' OR phelps LIKE 'UH%')
		GROUP BY phelps, name, LEFT(text,120), notes
		ORDER BY phelps
	`)
	var out []InventoryEntry
	for _, row := range rows[1:] {
		if len(row) < 5 {
			continue
		}
		// Determine prefix for categorization
		prefix := "TMP"
		pin := row[0]
		if strings.HasPrefix(pin, "XAB") {
			prefix = "XAB"
		} else if strings.HasPrefix(pin, "XBH") {
			prefix = "XBH"
		} else if strings.HasPrefix(pin, "XBB") {
			prefix = "XBB"
		} else if strings.HasPrefix(pin, "UHR") {
			prefix = "UHR"
		} else if strings.HasPrefix(pin, "UH") {
			prefix = "UH"
		}
		// Strip HTML from first line
		firstLine := row[2]
		firstLine = strings.ReplaceAll(firstLine, "<p>", "")
		firstLine = strings.ReplaceAll(firstLine, "</p>", "")
		firstLine = strings.ReplaceAll(firstLine, "<br>", " ")
		// Trim to first sentence
		if idx := strings.Index(firstLine, ". "); idx > 0 && idx < 100 {
			firstLine = firstLine[:idx+1]
		}
		out = append(out, InventoryEntry{
			PIN:       pin,
			Title:     row[1],
			FirstLine: firstLine,
			Notes:     row[3],
			Prefix:    prefix,
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

// stripHTML removes HTML tags and collapses whitespace.
func stripHTML(s string) string {
	out := make([]byte, 0, len(s))
	inTag := false
	for i := 0; i < len(s); i++ {
		if s[i] == '<' {
			inTag = true
		} else if s[i] == '>' {
			inTag = false
			out = append(out, ' ')
		} else if !inTag {
			out = append(out, s[i])
		}
	}
	result := strings.Join(strings.Fields(string(out)), " ")
	return strings.TrimSpace(result)
}
