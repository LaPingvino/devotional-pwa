// gen_quran_data.go — queries Dolt and writes Qur'an JSON data files for Hugo build
//
// Usage:
//   go run gen_quran_data.go [--dolt-dir ~/bahaiwritings] --out-dir /path/to/hugo-site
//
// Outputs (relative to out-dir):
//   assets/quran/{lang}.json        — {language, surahs: [{number, name_ar, name_trans, verses, commentary}, ...]}
//   data/quran_languages.json       — [{code, name}, ...]

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
	outDir  = flag.String("out-dir", "", "Hugo site root (required)")
)

// QuranLanguage for the languages index file
type QuranLanguage struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Verse holds one ayah
type Verse struct {
	Ayah int    `json:"ayah"`
	Text string `json:"text"`
}

// Surah holds one surah with its verses and optional commentary
type Surah struct {
	Number    int     `json:"number"`
	NameAr    string  `json:"name_ar"`
	NameTrans string  `json:"name_trans"`
	Verses    []Verse `json:"verses"`
	Commentary string `json:"commentary,omitempty"`
}

// QuranLangFile is the top-level structure for per-language JSON
type QuranLangFile struct {
	Language string  `json:"language"`
	Surahs   []Surah `json:"surahs"`
}

func main() {
	flag.Parse()
	if *outDir == "" {
		log.Fatal("--out-dir is required")
	}
	log.Printf("Dolt repo: %s", *doltDir)
	log.Printf("Hugo site: %s", *outDir)

	quranDir := filepath.Join(*outDir, "assets", "quran")
	dataDir := filepath.Join(*outDir, "data")
	for _, dir := range []string{quranDir, dataDir} {
		must(os.MkdirAll(dir, 0755))
	}

	// 1. Query available languages
	log.Println("-> querying Qur'an languages...")
	langs := queryQuranLanguages()
	log.Printf("  %d languages", len(langs))
	writeJSON(filepath.Join(dataDir, "quran_languages.json"), langs)

	// 2. Query all verses (single bulk query)
	log.Println("-> querying all verses...")
	allVerses := queryAllVerses()

	// 3. Query all commentaries
	log.Println("-> querying commentaries...")
	commentaries := queryCommentaries()

	// 4. Write per-language files
	log.Println("-> writing per-language files...")
	for _, lang := range langs {
		lc := lang.Code
		surahMap := allVerses[lc]
		if surahMap == nil {
			log.Printf("  %s: no verses, skipping", lc)
			continue
		}

		// Build ordered list of surahs (1..114)
		surahs := make([]Surah, 0, 114)
		for num := 1; num <= 114; num++ {
			sv, ok := surahMap[num]
			if !ok {
				continue
			}
			s := Surah{
				Number:    num,
				NameAr:    sv.nameAr,
				NameTrans: sv.nameTrans,
				Verses:    sv.verses,
			}
			if c, ok := commentaries[lc][num]; ok {
				s.Commentary = c
			}
			surahs = append(surahs, s)
		}

		qf := QuranLangFile{
			Language: lc,
			Surahs:  surahs,
		}
		writeJSON(filepath.Join(quranDir, lc+".json"), qf)
		totalVerses := 0
		for _, s := range surahs {
			totalVerses += len(s.Verses)
		}
		log.Printf("  %s: %d surahs, %d verses", lc, len(surahs), totalVerses)
	}

	log.Println("Done!")
}

// langNameMap maps language codes to English display names
var langNameMap = map[string]string{
	"ar": "العربية",
	"cu": "Словѣньскъ",
	"en": "English",
	"eo": "Esperanto",
	"fa": "فارسی",
	"fr": "Fran\u00e7ais",
	"fy": "Frysk",
	"id": "Bahasa Indonesia",
	"nl": "Nederlands",
	"pt": "Português",
	"ru": "Русский",
	"uk": "Українська",
}

func queryQuranLanguages() []QuranLanguage {
	rows := doltQuery(`
		SELECT DISTINCT language
		FROM quran
		ORDER BY language
	`)
	var out []QuranLanguage
	for _, row := range rows[1:] {
		if len(row) < 1 {
			continue
		}
		code := row[0]
		name := langNameMap[code]
		if name == "" {
			name = code
		}
		out = append(out, QuranLanguage{Code: code, Name: name})
	}
	return out
}

// surahData collects verses and metadata for one surah
type surahData struct {
	nameAr    string
	nameTrans string
	verses    []Verse
}

// queryAllVerses returns lang -> surah number -> surahData (with ordered verses)
func queryAllVerses() map[string]map[int]*surahData {
	rows := doltQuery(`
		SELECT language, surah, ayah,
		       COALESCE(surah_name_ar,''),
		       COALESCE(surah_name_trans,''),
		       COALESCE(text,'')
		FROM quran
		ORDER BY language, surah, ayah
	`)

	result := map[string]map[int]*surahData{}
	for _, row := range rows[1:] {
		if len(row) < 6 {
			continue
		}
		lang := row[0]
		var surahNum, ayahNum int
		fmt.Sscanf(row[1], "%d", &surahNum)
		fmt.Sscanf(row[2], "%d", &ayahNum)
		nameAr := row[3]
		nameTrans := row[4]
		text := row[5]

		if result[lang] == nil {
			result[lang] = map[int]*surahData{}
		}
		sd, ok := result[lang][surahNum]
		if !ok {
			sd = &surahData{nameAr: nameAr, nameTrans: nameTrans}
			result[lang][surahNum] = sd
		}
		sd.verses = append(sd.verses, Verse{Ayah: ayahNum, Text: text})
	}
	return result
}

// queryCommentaries returns lang -> surah number -> commentary text
func queryCommentaries() map[string]map[int]string {
	rows := doltQuery(`
		SELECT language, surah, COALESCE(commentary,'')
		FROM quran_comments
		ORDER BY language, surah
	`)

	result := map[string]map[int]string{}
	for _, row := range rows[1:] {
		if len(row) < 3 {
			continue
		}
		lang := row[0]
		var surahNum int
		fmt.Sscanf(row[1], "%d", &surahNum)
		commentary := row[2]
		if commentary == "" {
			continue
		}
		if result[lang] == nil {
			result[lang] = map[int]string{}
		}
		result[lang][surahNum] = commentary
	}
	return result
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
