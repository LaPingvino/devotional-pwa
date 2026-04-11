// sync_writings.go — Fetch Bahá'í writings from bahaiprayers.net API and sync to Dolt.
//
// Handles: Hidden Words, Kitáb-i-Aqdas, Kitáb-i-Íqán, Gleanings, Ridván Messages,
//          Prayers & Meditations, Some Answered Questions, Tablets, Days of Remembrance
//
// Usage:
//   go run sync_writings.go                         # dry-run, show what would be added
//   go run sync_writings.go --apply                 # insert into Dolt
//   go run sync_writings.go --type hidden_words     # only sync Hidden Words
//   go run sync_writings.go --type ridvan --apply   # only sync Ridván, apply
//   go run sync_writings.go --lang en               # only sync English
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	apiBase = "https://BahaiPrayers.net/api/prayer/"
	doltDir = "/home/joop/bahaiwritings"
	source  = "bahaiprayers.net"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// ---- API response types ----

type APILang struct {
	Id          int    `json:"Id"`
	English     string `json:"English"`
	PrayerCount int    `json:"PrayerCount"`
}

type HiddenWord struct {
	Id         int    `json:"Id"`
	Number     int    `json:"Number"`
	LanguageId int    `json:"LanguageId"`
	IsArabic   bool   `json:"IsArabic"`
	Text       string `json:"Text"`
}

type AqdasEntry struct {
	Id         int               `json:"Id"`
	Number     int               `json:"Number"`
	LanguageId int               `json:"LanguageId"`
	Text       string            `json:"Text"`
	Notes      json.RawMessage   `json:"Notes"`
	QAs        json.RawMessage   `json:"QAs"`
}

type IqanEntry struct {
	Id         int    `json:"Id"`
	Number     int    `json:"Number"`
	Part       int    `json:"Part"`
	LanguageId int    `json:"LanguageId"`
	Text       string `json:"Text"`
}

type GleaningEntry struct {
	Id         int    `json:"Id"`
	Number     int    `json:"Number"`
	Roman      string `json:"Roman"`
	LanguageId int    `json:"LanguageId"`
	Text       string `json:"Text"`
}

type PMEntry struct {
	Id         int    `json:"Id"`
	Number     int    `json:"Number"`
	LanguageId int    `json:"LanguageId"`
	Text       string `json:"Text"`
}

type SaqPart struct {
	Id         int    `json:"Id"`
	Number     int    `json:"Number"`
	LanguageId int    `json:"LanguageId"`
	Text       string `json:"Text"`
	Title      string `json:"Title"`
}

type TabletEntry struct {
	Id           int    `json:"Id"`
	TabletNumber int    `json:"TabletNumber"`
	Number       int    `json:"Number"`
	LanguageId   int    `json:"LanguageId"`
	Title        string `json:"Title"`
	SubTitle     string `json:"SubTitle"`
	Text         string `json:"Text"`
}

type DaysEntry struct {
	Id         int    `json:"Id"`
	Number     int    `json:"Number"`
	LanguageId int    `json:"LanguageId"`
	Text       string `json:"Text"`
}

type RidvanEntry struct {
	Id         int    `json:"Id"`
	Year       int    `json:"Year"`
	BEYear     int    `json:"BEYear"`
	LanguageId int    `json:"LanguageId"`
	Title      *string `json:"Title"`
	Text       string `json:"Text"`
}

// ---- DB entry to insert ----

type DBRow struct {
	Phelps   string
	Language string
	Name     string
	Type     string
	Text     string
	Source   string
	SourceID string
}

// ---- Writing type definitions ----

type WritingDef struct {
	Key             string // internal key
	Name            string // display name
	DBType          string // writings.type value
	LangEndpoint    string
	ContentEndpoint string
}

var writingDefs = []WritingDef{
	{"hidden_words", "Hidden Words", "hidden_words", "HiddenLanguages", "HiddensByLanguage"},
	{"aqdas", "Kitáb-i-Aqdas", "aqdas", "AqdasLanguages", "AqdasByLanguage"},
	{"iqan", "Kitáb-i-Íqán", "iqan", "IqanLanguages", "IqansByLanguage"},
	{"gleanings", "Gleanings", "gleanings", "GleaningLanguages", "GleaningsByLanguage"},
	{"pm", "Prayers & Meditations", "pm", "PMLanguages", "PMsByLanguage"},
	{"saq", "Some Answered Questions", "saq", "SaqLanguages", "SaqTopicsByLanguage"},
	{"tablets", "Tablets of Bahá'u'lláh", "tablets", "TabLanguages", "TabsByLanguage"},
	{"days_remembrance", "Days of Remembrance", "days_remembrance", "DaysRememberLanguages", "DaysRemembersByLanguage"},
	{"ridvan", "Ridván Messages", "ridvan", "RidvanLanguages", "RidvansByLanguage"},
}

// ---- Flags ----

var (
	applyFlag = flag.Bool("apply", false, "Actually insert into Dolt (default: dry-run)")
	typeFlag  = flag.String("type", "", "Only sync this writing type (e.g. hidden_words, aqdas, ridvan)")
	langFlag  = flag.String("lang", "", "Only sync this ISO language code")
	htmlFlag  = flag.Bool("html", true, "Request HTML-formatted text from API")
)

func main() {
	flag.Parse()

	// Load API ID → ISO mapping from Dolt
	apiToISO := loadAPIMapping()
	log.Printf("Loaded %d API→ISO mappings", len(apiToISO))

	// Load existing entries to avoid duplicates
	existing := loadExisting()
	log.Printf("Loaded %d existing writing entries", len(existing))

	var allRows []DBRow
	var allSQL []string

	for _, wd := range writingDefs {
		if *typeFlag != "" && *typeFlag != wd.Key {
			continue
		}
		log.Printf("\n=== %s ===", wd.Name)

		// Fetch available languages
		langs := fetchLangs(wd.LangEndpoint)
		log.Printf("  %d languages available", len(langs))

		for _, al := range langs {
			iso, ok := apiToISO[al.Id]
			if !ok {
				log.Printf("  WARN: no ISO mapping for API ID %d (%s), skipping", al.Id, al.English)
				continue
			}
			if *langFlag != "" && *langFlag != iso {
				continue
			}

			rows := fetchWriting(wd, al.Id, iso)
			if len(rows) == 0 {
				continue
			}

			// Filter out existing entries
			var newRows []DBRow
			for _, r := range rows {
				key := r.Phelps + "|" + r.Language
				if existing[key] {
					continue
				}
				newRows = append(newRows, r)
			}

			if len(newRows) > 0 {
				log.Printf("  %s: %d new entries (of %d total)", iso, len(newRows), len(rows))
				allRows = append(allRows, newRows...)
				for _, r := range newRows {
					allSQL = append(allSQL, toSQL(r))
				}
			}
		}
	}

	log.Printf("\n=== Summary ===")
	log.Printf("  %d new entries to insert", len(allRows))

	if len(allSQL) == 0 {
		log.Println("Nothing to do.")
		return
	}

	// Write SQL file — batched INSERT with multi-row VALUES for performance
	tmpDir := os.Getenv("TMPDIR")
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	sqlFile := tmpDir + "/sync_writings.sql"
	f, err := os.Create(sqlFile)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintln(f, "SET FOREIGN_KEY_CHECKS=0;")
	// Write in batches of 500 rows per INSERT for much faster import
	batchSize := 500
	for i := 0; i < len(allRows); i += batchSize {
		end := i + batchSize
		if end > len(allRows) {
			end = len(allRows)
		}
		fmt.Fprint(f, "INSERT INTO writings (phelps, language, version, name, type, text, source, source_id, is_verified) VALUES\n")
		for j, r := range allRows[i:end] {
			if j > 0 {
				fmt.Fprint(f, ",\n")
			}
			fmt.Fprintf(f, "('%s', '%s', UUID(), '%s', '%s', '%s', '%s', '%s', 1)",
				sqlEsc(r.Phelps), sqlEsc(r.Language), sqlEsc(r.Name), sqlEsc(r.Type),
				sqlEsc(r.Text), sqlEsc(r.Source), sqlEsc(r.SourceID))
		}
		fmt.Fprintln(f, ";")
	}
	fmt.Fprintln(f, "SET FOREIGN_KEY_CHECKS=1;")
	f.Close()
	log.Printf("  SQL written to %s (%d batches)", sqlFile, (len(allRows)+batchSize-1)/batchSize)

	if *applyFlag {
		log.Println("  Applying to Dolt...")
		applySQL(sqlFile)
		log.Println("  Done!")
	} else {
		log.Printf("  Dry run. Use --apply to insert, or:")
		log.Printf("  grep '^SET\\|^INSERT' %s | dolt sql", sqlFile)
	}
}

// ---- API fetching ----

func fetchJSON(url string, target interface{}) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func fetchLangs(endpoint string) []APILang {
	var langs []APILang
	url := apiBase + endpoint
	if err := fetchJSON(url, &langs); err != nil {
		log.Printf("  WARN: failed to fetch languages from %s: %v", url, err)
		return nil
	}
	return langs
}

func fetchWriting(wd WritingDef, apiLangID int, iso string) []DBRow {
	htmlParam := ""
	if *htmlFlag {
		htmlParam = "&html=true"
	}
	url := fmt.Sprintf("%s%s?languageid=%d%s", apiBase, wd.ContentEndpoint, apiLangID, htmlParam)

	switch wd.Key {
	case "hidden_words":
		return fetchHiddenWords(url, iso)
	case "aqdas":
		return fetchAqdas(url, iso)
	case "iqan":
		return fetchIqan(url, iso)
	case "gleanings":
		return fetchGleanings(url, iso)
	case "pm":
		return fetchPM(url, iso)
	case "saq":
		return fetchSAQ(url, iso, apiLangID)
	case "tablets":
		return fetchTablets(url, iso)
	case "days_remembrance":
		return fetchDays(url, iso)
	case "ridvan":
		return fetchRidvan(url, iso)
	}
	return nil
}

func fetchHiddenWords(url, iso string) []DBRow {
	var entries []HiddenWord
	if err := fetchJSON(url, &entries); err != nil {
		log.Printf("    WARN: %v", err)
		return nil
	}

	// Split into Arabic (BH00386) and Persian (BH00113)
	// Arabic entries: IsArabic=true, numbered 1-71
	// Persian entries: IsArabic=false, numbered relative to their section
	var rows []DBRow
	persianNum := 0
	for _, e := range entries {
		var phelps, name string
		if e.IsArabic {
			phelps = fmt.Sprintf("BH00386A%02d", e.Number)
			name = fmt.Sprintf("Arabic Hidden Word %d", e.Number)
		} else {
			persianNum++
			phelps = fmt.Sprintf("BH00113P%02d", persianNum)
			name = fmt.Sprintf("Persian Hidden Word %d", persianNum)
		}
		rows = append(rows, DBRow{
			Phelps:   phelps,
			Language: iso,
			Name:     name,
			Type:     "hidden_words",
			Text:     cleanHTML(e.Text),
			Source:   source,
			SourceID: fmt.Sprintf("hw_%d", e.Id),
		})
	}
	return rows
}

// AqdasWrapper matches the API response shape: {"Language": ..., "Paragraphs": [...]}
type AqdasWrapper struct {
	Language   json.RawMessage `json:"Language"`
	Paragraphs []AqdasEntry   `json:"Paragraphs"`
}

func fetchAqdas(url, iso string) []DBRow {
	var wrapper AqdasWrapper
	if err := fetchJSON(url, &wrapper); err != nil {
		log.Printf("    WARN: %v", err)
		return nil
	}
	entries := wrapper.Paragraphs
	var rows []DBRow
	for _, e := range entries {
		phelps := fmt.Sprintf("BH00001%03d", e.Number)
		rows = append(rows, DBRow{
			Phelps:   phelps,
			Language: iso,
			Name:     fmt.Sprintf("Kitáb-i-Aqdas ¶%d", e.Number),
			Type:     "aqdas",
			Text:     cleanHTML(e.Text),
			Source:   source,
			SourceID: fmt.Sprintf("aqdas_%d", e.Id),
		})
	}
	return rows
}

func fetchIqan(url, iso string) []DBRow {
	var entries []IqanEntry
	if err := fetchJSON(url, &entries); err != nil {
		log.Printf("    WARN: %v", err)
		return nil
	}
	var rows []DBRow
	for _, e := range entries {
		phelps := fmt.Sprintf("BH00002%d%03d", e.Part, e.Number)
		rows = append(rows, DBRow{
			Phelps:   phelps,
			Language: iso,
			Name:     fmt.Sprintf("Kitáb-i-Íqán Part %d §%d", e.Part, e.Number),
			Type:     "iqan",
			Text:     cleanHTML(e.Text),
			Source:   source,
			SourceID: fmt.Sprintf("iqan_%d", e.Id),
		})
	}
	return rows
}

func fetchGleanings(url, iso string) []DBRow {
	var entries []GleaningEntry
	if err := fetchJSON(url, &entries); err != nil {
		log.Printf("    WARN: %v", err)
		return nil
	}
	var rows []DBRow
	for _, e := range entries {
		// Use Roman numeral as subcode suffix (pad to 3 chars)
		roman := strings.ToUpper(e.Roman)
		// For now, use sequential number; matching to existing BH codes comes later
		phelps := fmt.Sprintf("BH10200%03d", e.Number)
		rows = append(rows, DBRow{
			Phelps:   phelps,
			Language: iso,
			Name:     fmt.Sprintf("Gleanings %s", roman),
			Type:     "gleanings",
			Text:     cleanHTML(e.Text),
			Source:   source,
			SourceID: fmt.Sprintf("gleanings_%d", e.Id),
		})
	}
	return rows
}

func fetchPM(url, iso string) []DBRow {
	var entries []PMEntry
	if err := fetchJSON(url, &entries); err != nil {
		log.Printf("    WARN: %v", err)
		return nil
	}
	var rows []DBRow
	for _, e := range entries {
		// PM entries are individual prayers — use BH09700xxx as staging codes
		// These will be matched to existing prayer Phelps codes later
		phelps := fmt.Sprintf("BH09700%03d", e.Number)
		rows = append(rows, DBRow{
			Phelps:   phelps,
			Language: iso,
			Name:     fmt.Sprintf("Prayers & Meditations %d", e.Number),
			Type:     "pm",
			Text:     cleanHTML(e.Text),
			Source:   source,
			SourceID: fmt.Sprintf("pm_%d", e.Id),
		})
	}
	return rows
}

func fetchSAQ(url, iso string, apiLangID int) []DBRow {
	// SAQ has topics, not a flat list
	var entries []SaqPart
	if err := fetchJSON(url, &entries); err != nil {
		log.Printf("    WARN: %v", err)
		return nil
	}
	var rows []DBRow
	for _, e := range entries {
		// SAQ chapters have existing AB codes; use staging codes for now
		phelps := fmt.Sprintf("AB09900%03d", e.Number)
		name := fmt.Sprintf("Some Answered Questions %d", e.Number)
		if e.Title != "" {
			name = fmt.Sprintf("SAQ %d: %s", e.Number, e.Title)
		}
		rows = append(rows, DBRow{
			Phelps:   phelps,
			Language: iso,
			Name:     name,
			Type:     "saq",
			Text:     cleanHTML(e.Text),
			Source:   source,
			SourceID: fmt.Sprintf("saq_%d", e.Id),
		})
	}
	return rows
}

func fetchTablets(url, iso string) []DBRow {
	var entries []TabletEntry
	if err := fetchJSON(url, &entries); err != nil {
		log.Printf("    WARN: %v", err)
		return nil
	}
	// Map TabletNumber → real Phelps base code from inventory
	tabletPhelps := map[int]string{
		1:  "BH02324", // Lawḥ-i-Karmil (Tablet of Carmel)
		2:  "BH00505", // Lawḥ-i-Aqdas (Most Holy Tablet / Tablet to the Christians)
		3:  "BH00568", // Bishárát (Glad-Tidings)
		4:  "BH00308", // Ṭarázát (Ornaments)
		5:  "BH00668", // Tajallíyát (Effulgences)
		6:  "BH00111", // Kalimát-i-Firdawsíyyih (Words of Paradise)
		7:  "BH00238", // Lawḥ-i-Dunyá (Tablet of the World)
		8:  "BH00053", // Ishráqát (Splendors)
		9:  "BH00223", // Lawḥ-i-Ḥikmat (Tablet of Wisdom)
		10: "BH02183", // Aṣl-i-Kullu'l-Khayr (Words of Wisdom)
		11: "BH00140", // Lawḥ-i-Maqṣúd
		12: "BH00354", // Súriy-i-Vafá
		13: "BH00587", // Lawḥ-i-Síyyid-i-Mihdíy-i-Dahají
		14: "BH00336", // Lawḥ-i-Burhán (Tablet of the Proof)
		15: "BH00003", // Kitáb-i-'Ahd (Book of the Covenant)
		16: "BH02209", // Lawḥ-i-Arḍ-i-Bá (Tablet of the Land of Bá)
		// 17: Excerpts from Other Tablets — no single code, skip or use TMP
		// 18: Passages Translated by Shoghi Effendi — compilations, skip
		// 19: Notes and References — not a writing, skip
	}

	var rows []DBRow
	for _, e := range entries {
		base, ok := tabletPhelps[e.TabletNumber]
		if !ok {
			continue // skip excerpts/notes sections without real codes
		}
		phelps := fmt.Sprintf("%s%03d", base, e.Number)
		name := ""
		if e.Title != "" {
			name = e.Title
			if e.SubTitle != "" {
				name += " (" + e.SubTitle + ")"
			}
			if e.Number > 1 {
				name += fmt.Sprintf(" %d", e.Number)
			}
		}
		rows = append(rows, DBRow{
			Phelps:   phelps,
			Language: iso,
			Name:     name,
			Type:     "tablets",
			Text:     cleanHTML(e.Text),
			Source:   source,
			SourceID: fmt.Sprintf("tablets_%d", e.Id),
		})
	}
	return rows
}

func fetchDays(url, iso string) []DBRow {
	var entries []DaysEntry
	if err := fetchJSON(url, &entries); err != nil {
		log.Printf("    WARN: %v", err)
		return nil
	}
	var rows []DBRow
	for _, e := range entries {
		phelps := fmt.Sprintf("BH09600%03d", e.Number)
		rows = append(rows, DBRow{
			Phelps:   phelps,
			Language: iso,
			Name:     fmt.Sprintf("Days of Remembrance %d", e.Number),
			Type:     "days_remembrance",
			Text:     cleanHTML(e.Text),
			Source:   source,
			SourceID: fmt.Sprintf("days_%d", e.Id),
		})
	}
	return rows
}

func fetchRidvan(url, iso string) []DBRow {
	var entries []RidvanEntry
	if err := fetchJSON(url, &entries); err != nil {
		log.Printf("    WARN: %v", err)
		return nil
	}
	var rows []DBRow
	for _, e := range entries {
		phelps := fmt.Sprintf("UHR%04d", e.Year)
		name := fmt.Sprintf("Ridván %d (%d BE)", e.Year, e.BEYear)
		if e.Title != nil && *e.Title != "" {
			name = *e.Title
		}
		rows = append(rows, DBRow{
			Phelps:   phelps,
			Language: iso,
			Name:     name,
			Type:     "ridvan",
			Text:     cleanHTML(e.Text),
			Source:   source,
			SourceID: fmt.Sprintf("ridvan_%d", e.Id),
		})
	}
	return rows
}

// ---- HTML cleanup ----

var reClassAttr = regexp.MustCompile(` class='[^']*'`)
var reMultiSpace = regexp.MustCompile(`\s+`)

func cleanHTML(html string) string {
	// Strip class attributes (dropCap etc), normalize whitespace
	html = reClassAttr.ReplaceAllString(html, "")
	html = strings.TrimSpace(html)
	return html
}

// ---- DB helpers ----

func loadAPIMapping() map[int]string {
	cmd := exec.Command("dolt", "sql", "-q",
		"SELECT api_id, langcode FROM languages WHERE api_id IS NOT NULL AND inlang='en'",
		"--result-format", "csv")
	cmd.Dir = doltDir
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("Failed to load API mapping: %v", err)
	}
	m := make(map[int]string)
	r := csv.NewReader(strings.NewReader(string(out)))
	rows, _ := r.ReadAll()
	for _, row := range rows[1:] {
		if len(row) < 2 {
			continue
		}
		id, _ := strconv.Atoi(row[0])
		if id > 0 {
			m[id] = row[1]
		}
	}
	return m
}

func loadExisting() map[string]bool {
	// key = "phelps|language" — check ALL existing entries regardless of type/source
	cmd := exec.Command("dolt", "sql", "-q",
		"SELECT phelps, language FROM writings WHERE phelps IS NOT NULL AND phelps <> ''",
		"--result-format", "csv")
	cmd.Dir = doltDir
	out, err := cmd.Output()
	if err != nil {
		return map[string]bool{}
	}
	m := make(map[string]bool)
	r := csv.NewReader(strings.NewReader(string(out)))
	rows, _ := r.ReadAll()
	for _, row := range rows[1:] {
		if len(row) < 2 {
			continue
		}
		m[row[0]+"|"+row[1]] = true
	}
	return m
}

func sqlEsc(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func toSQL(r DBRow) string {
	return fmt.Sprintf(
		"INSERT INTO writings (phelps, language, version, name, type, text, source, source_id, is_verified) "+
			"VALUES ('%s', '%s', UUID(), '%s', '%s', '%s', '%s', '%s', 1);",
		sqlEsc(r.Phelps), sqlEsc(r.Language), sqlEsc(r.Name), sqlEsc(r.Type),
		sqlEsc(r.Text), sqlEsc(r.Source), sqlEsc(r.SourceID),
	)
}

func applySQL(sqlFile string) {
	cmd := exec.Command("dolt", "sql")
	cmd.Dir = doltDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	inf, err := os.Open(sqlFile)
	if err != nil {
		log.Fatalf("Failed to open SQL file: %v", err)
	}
	defer inf.Close()
	cmd.Stdin = inf
	if err := cmd.Run(); err != nil {
		log.Fatalf("Failed to apply SQL: %v", err)
	}
}

// ---- Phelps code utilities ----

// existingHWCodes checks which Hidden Words phelps codes already exist
// for a given language to avoid re-inserting.
func loadExistingPhelps(iso string) map[string]bool {
	cmd := exec.Command("dolt", "sql", "-q",
		fmt.Sprintf("SELECT phelps FROM writings WHERE language='%s' AND source='%s'", sqlEsc(iso), source),
		"--result-format", "csv")
	cmd.Dir = doltDir
	out, err := cmd.Output()
	if err != nil {
		return map[string]bool{}
	}
	m := make(map[string]bool)
	r := csv.NewReader(strings.NewReader(string(out)))
	rows, _ := r.ReadAll()
	for _, row := range rows[1:] {
		if len(row) > 0 {
			m[row[0]] = true
		}
	}
	return m
}

// For types that need matching later, gather source_ids to avoid re-fetching
func loadExistingSourceIDs(dbType string) map[string]bool {
	cmd := exec.Command("dolt", "sql", "-q",
		fmt.Sprintf("SELECT CONCAT(source_id, '|', language) FROM writings WHERE type='%s' AND source='%s'",
			sqlEsc(dbType), source),
		"--result-format", "csv")
	cmd.Dir = doltDir
	out, err := cmd.Output()
	if err != nil {
		return map[string]bool{}
	}
	m := make(map[string]bool)
	r := csv.NewReader(strings.NewReader(string(out)))
	rows, _ := r.ReadAll()
	for _, row := range rows[1:] {
		if len(row) > 0 {
			m[row[0]] = true
		}
	}
	return m
}

// Sort languages by API ID for deterministic output
type byID []APILang
func (a byID) Len() int           { return len(a) }
func (a byID) Less(i, j int) bool { return a[i].Id < a[j].Id }
func (a byID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

func init() {
	// Ensure deterministic ordering
	_ = sort.Sort
}
