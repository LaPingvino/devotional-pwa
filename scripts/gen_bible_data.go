// gen_bible_data.go — Generate Hugo data files from bible-root markdown chapters.
//
// Reads: ~/bible-root/{en,ar}/... markdown files (KJV + Van Dyck 1865)
//        ~/bible-root/{he,syr,el}/... if present (source languages)
// Writes: Hugo data + assets for /bible/ section
//
// Usage:
//   go run gen_bible_data.go --bible-dir ~/bible-root --out-dir ~/prayermatching/devotional-pwa

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Book metadata: Hebrew name, conventional name, section, order
type BookMeta struct {
	ID         string `json:"id"`          // slug: genesis, matthew, etc.
	Hebrew     string `json:"hebrew"`      // Hebrew/Aramaic name
	Convention string `json:"conventional"` // Conventional English name
	Section    string `json:"section"`     // torah, neviim, ketuvim, nt
	SectionName string `json:"section_name"` // Torah, Nevi'im, Ketuvim, New Testament
	Order      int    `json:"order"`       // display order within section
	Chapters   int    `json:"chapters"`    // number of chapters
	HasHebrew  bool   `json:"has_hebrew"`
	HasSyriac  bool   `json:"has_syriac"`
	HasGreek   bool   `json:"has_greek"`
	HasEnglish bool   `json:"has_english"`
	HasArabic  bool   `json:"has_arabic"`
}

// Chapter data for JSON output
type ChapterData struct {
	Book     string  `json:"book"`
	Chapter  int     `json:"chapter"`
	Title    string  `json:"title"`
	Subtitle string  `json:"subtitle"`
	Verses   []Verse `json:"verses"`
}

type Verse struct {
	Num     int    `json:"num"`
	// Source languages
	Hebrew  string `json:"he,omitempty"`
	Syriac  string `json:"syr,omitempty"`
	Greek   string `json:"el,omitempty"`
	// Translation languages
	English string `json:"en,omitempty"`
	Arabic  string `json:"ar,omitempty"`
}

// Canonical book ordering — Tanakh order, then New Testament.
// IDs match the conventional English filenames in bible-root.
var bookOrder = []struct {
	id, hebrew, conventional, section, sectionName string
}{
	// Torah
	{"genesis", "בראשית", "Genesis", "torah", "Torah"},
	{"exodus", "שמות", "Exodus", "torah", "Torah"},
	{"leviticus", "ויקרא", "Leviticus", "torah", "Torah"},
	{"numbers", "במדבר", "Numbers", "torah", "Torah"},
	{"deuteronomy", "דברים", "Deuteronomy", "torah", "Torah"},
	// Nevi'im (Prophets)
	{"joshua", "יהושע", "Joshua", "neviim", "Nevi'im"},
	{"judges", "שופטים", "Judges", "neviim", "Nevi'im"},
	{"1-samuel", "שמואל א", "1 Samuel", "neviim", "Nevi'im"},
	{"2-samuel", "שמואל ב", "2 Samuel", "neviim", "Nevi'im"},
	{"1-kings", "מלכים א", "1 Kings", "neviim", "Nevi'im"},
	{"2-kings", "מלכים ב", "2 Kings", "neviim", "Nevi'im"},
	{"isaiah", "ישעיהו", "Isaiah", "neviim", "Nevi'im"},
	{"jeremiah", "ירמיהו", "Jeremiah", "neviim", "Nevi'im"},
	{"ezekiel", "יחזקאל", "Ezekiel", "neviim", "Nevi'im"},
	{"hosea", "הושע", "Hosea", "neviim", "Nevi'im"},
	{"joel", "יואל", "Joel", "neviim", "Nevi'im"},
	{"amos", "עמוס", "Amos", "neviim", "Nevi'im"},
	{"obadiah", "עובדיה", "Obadiah", "neviim", "Nevi'im"},
	{"jonah", "יונה", "Jonah", "neviim", "Nevi'im"},
	{"micah", "מיכה", "Micah", "neviim", "Nevi'im"},
	{"nahum", "נחום", "Nahum", "neviim", "Nevi'im"},
	{"habakkuk", "חבקוק", "Habakkuk", "neviim", "Nevi'im"},
	{"zephaniah", "צפניה", "Zephaniah", "neviim", "Nevi'im"},
	{"haggai", "חגי", "Haggai", "neviim", "Nevi'im"},
	{"zechariah", "זכריה", "Zechariah", "neviim", "Nevi'im"},
	{"malachi", "מלאכי", "Malachi", "neviim", "Nevi'im"},
	// Ketuvim (Writings)
	{"psalms", "תהלים", "Psalms", "ketuvim", "Ketuvim"},
	{"proverbs", "משלי", "Proverbs", "ketuvim", "Ketuvim"},
	{"job", "איוב", "Job", "ketuvim", "Ketuvim"},
	{"song-of-solomon", "שיר השירים", "Song of Solomon", "ketuvim", "Ketuvim"},
	{"ruth", "רות", "Ruth", "ketuvim", "Ketuvim"},
	{"lamentations", "איכה", "Lamentations", "ketuvim", "Ketuvim"},
	{"ecclesiastes", "קהלת", "Ecclesiastes", "ketuvim", "Ketuvim"},
	{"esther", "אסתר", "Esther", "ketuvim", "Ketuvim"},
	{"daniel", "דניאל", "Daniel", "ketuvim", "Ketuvim"},
	{"ezra", "עזרא", "Ezra", "ketuvim", "Ketuvim"},
	{"nehemiah", "נחמיה", "Nehemiah", "ketuvim", "Ketuvim"},
	{"1-chronicles", "דברי הימים א", "1 Chronicles", "ketuvim", "Ketuvim"},
	{"2-chronicles", "דברי הימים ב", "2 Chronicles", "ketuvim", "Ketuvim"},
	// New Testament
	{"matthew", "Ματθαῖος", "Matthew", "nt", "New Testament"},
	{"mark", "Μᾶρκος", "Mark", "nt", "New Testament"},
	{"luke", "Λουκᾶς", "Luke", "nt", "New Testament"},
	{"john", "Ἰωάννης", "John", "nt", "New Testament"},
	{"acts", "Πράξεις", "Acts", "nt", "New Testament"},
	{"romans", "Ῥωμαίους", "Romans", "nt", "New Testament"},
	{"1-corinthians", "Κορινθίους Αʹ", "1 Corinthians", "nt", "New Testament"},
	{"2-corinthians", "Κορινθίους Βʹ", "2 Corinthians", "nt", "New Testament"},
	{"galatians", "Γαλάτας", "Galatians", "nt", "New Testament"},
	{"ephesians", "Ἐφεσίους", "Ephesians", "nt", "New Testament"},
	{"philippians", "Φιλιππησίους", "Philippians", "nt", "New Testament"},
	{"colossians", "Κολοσσαεῖς", "Colossians", "nt", "New Testament"},
	{"1-thessalonians", "Θεσσαλονικεῖς Αʹ", "1 Thessalonians", "nt", "New Testament"},
	{"2-thessalonians", "Θεσσαλονικεῖς Βʹ", "2 Thessalonians", "nt", "New Testament"},
	{"1-timothy", "Τιμόθεον Αʹ", "1 Timothy", "nt", "New Testament"},
	{"2-timothy", "Τιμόθεον Βʹ", "2 Timothy", "nt", "New Testament"},
	{"titus", "Τίτον", "Titus", "nt", "New Testament"},
	{"philemon", "Φιλήμονα", "Philemon", "nt", "New Testament"},
	{"hebrews", "Ἑβραίους", "Hebrews", "nt", "New Testament"},
	{"james", "Ἰάκωβος", "James", "nt", "New Testament"},
	{"1-peter", "Πέτρου Αʹ", "1 Peter", "nt", "New Testament"},
	{"2-peter", "Πέτρου Βʹ", "2 Peter", "nt", "New Testament"},
	{"1-john", "Ἰωάννου Αʹ", "1 John", "nt", "New Testament"},
	{"2-john", "Ἰωάννου Βʹ", "2 John", "nt", "New Testament"},
	{"3-john", "Ἰωάννου Γʹ", "3 John", "nt", "New Testament"},
	{"jude", "Ἰούδας", "Jude", "nt", "New Testament"},
	{"revelation", "Ἀποκάλυψις", "Revelation", "nt", "New Testament"},
}

// Hebrew transliteration filenames → English book IDs
var hebrewToEnglish = map[string]string{
	// Torah
	"bereshit": "genesis", "shemot": "exodus", "vayikra": "leviticus",
	"bamidbar": "numbers", "devarim": "deuteronomy",
	// Nevi'im
	"yehoshua": "joshua", "shoftim": "judges",
	"shmuel-a": "1-samuel", "shmuel-b": "2-samuel",
	"melakhim-a": "1-kings", "melakhim-b": "2-kings",
	"yeshayahu": "isaiah", "yirmeyahu": "jeremiah", "yechezkel": "ezekiel",
	"hoshea": "hosea", "yoel": "joel", "amos": "amos",
	"ovadyah": "obadiah", "yonah": "jonah", "mikhah": "micah",
	"nachum": "nahum", "chavakuk": "habakkuk", "tzefanyah": "zephaniah",
	"chaggai": "haggai", "zekharyah": "zechariah", "malakhi": "malachi",
	// Ketuvim
	"tehillim": "psalms", "mishlei": "proverbs", "iyyov": "job",
	"shir-hashirim": "song-of-solomon", "rut": "ruth",
	"eikhah": "lamentations", "kohelet": "ecclesiastes",
	"ester": "esther", "daniel": "daniel", "ezra": "ezra",
	"nechemyah": "nehemiah",
	"divrei-hayamim-a": "1-chronicles", "divrei-hayamim-b": "2-chronicles",
}

// Build reverse map: English → Hebrew filename
var englishToHebrew = func() map[string]string {
	m := make(map[string]string, len(hebrewToEnglish))
	for he, en := range hebrewToEnglish {
		m[en] = he
	}
	return m
}()

// Greek/Syriac NT filenames use no hyphen (1corinthians vs 1-corinthians)
var englishToGreek = map[string]string{
	"1-corinthians":     "1corinthians",
	"2-corinthians":     "2corinthians",
	"1-thessalonians":   "1thessalonians",
	"2-thessalonians":   "2thessalonians",
	"1-timothy":         "1timothy",
	"2-timothy":         "2timothy",
	"1-peter":           "1peter",
	"2-peter":           "2peter",
	"1-john":            "1john",
	"2-john":            "2john",
	"3-john":            "3john",
}

var verseRe = regexp.MustCompile(`^(\d+)\.\s+(.+)`)
var verseReArabicNums = regexp.MustCompile(`^([٠-٩]+)\.\s+(.+)`)

// arabicToWestern converts Arabic-Indic numerals to Western digits.
func arabicToWestern(s string) int {
	n := 0
	for _, r := range s {
		if r >= '٠' && r <= '٩' {
			n = n*10 + int(r-'٠')
		}
	}
	return n
}

func parseChapter(path string) (title, subtitle string, verses []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", nil
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") && title == "" {
			title = strings.TrimPrefix(line, "# ")
		} else if strings.HasPrefix(line, "## ") && subtitle == "" {
			subtitle = strings.TrimPrefix(line, "## ")
		} else if verseRe.MatchString(line) || verseReArabicNums.MatchString(line) {
			verses = append(verses, line)
		}
	}
	return
}

func parseVerses(lines []string) map[int]string {
	result := make(map[int]string)
	for _, line := range lines {
		if m := verseRe.FindStringSubmatch(line); m != nil {
			num, _ := strconv.Atoi(m[1])
			result[num] = m[2]
		} else if m := verseReArabicNums.FindStringSubmatch(line); m != nil {
			num := arabicToWestern(m[1])
			result[num] = m[2]
		}
	}
	return result
}

func findChapters(dir, bookID string) []string {
	pattern := filepath.Join(dir, bookID+"-*.md")
	matches, _ := filepath.Glob(pattern)
	sort.Strings(matches)
	return matches
}

func main() {
	bibleDir := flag.String("bible-dir", os.ExpandEnv("$HOME/bible-root"), "Path to bible-root directory")
	outDir := flag.String("out-dir", os.ExpandEnv("$HOME/prayermatching/devotional-pwa"), "Hugo site root")
	flag.Parse()

	fmt.Fprintf(os.Stderr, "Bible dir: %s\n", *bibleDir)
	fmt.Fprintf(os.Stderr, "Output dir: %s\n", *outDir)

	// Determine source directories for each language
	langDirs := map[string]map[string]string{} // lang → section → dir
	allSections := []string{"torah", "neviim", "ketuvim", "nt"}

	// Primary languages: en (KJV), ar (Van Dyck 1865) — full Bible
	for _, lang := range []string{"en", "ar"} {
		for _, sec := range allSections {
			dir := filepath.Join(*bibleDir, lang, sec)
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				if langDirs[lang] == nil {
					langDirs[lang] = map[string]string{}
				}
				langDirs[lang][sec] = dir
			}
		}
	}
	// Optional source languages: Hebrew (OT), Syriac/Greek (NT)
	for _, sec := range []string{"torah", "neviim", "ketuvim"} {
		dir := filepath.Join(*bibleDir, "he", sec)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			if langDirs["he"] == nil {
				langDirs["he"] = map[string]string{}
			}
			langDirs["he"][sec] = dir
		}
	}
	for _, lang := range []string{"syr", "el"} {
		dir := filepath.Join(*bibleDir, lang, "nt")
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			langDirs[lang] = map[string]string{"nt": dir}
		}
	}

	// Build book metadata
	var books []BookMeta

	for i, b := range bookOrder {
		// Count chapters — English is the primary source for chapter counts
		maxCh := 0
		hasHe, hasSyr, hasEl, hasEn, hasAr := false, false, false, false, false

		// English (primary)
		if dirs, ok := langDirs["en"]; ok {
			if d, ok2 := dirs[b.section]; ok2 {
				chs := findChapters(d, b.id)
				if len(chs) > maxCh {
					maxCh = len(chs)
				}
				hasEn = len(chs) > 0
			}
		}
		// Arabic
		if dirs, ok := langDirs["ar"]; ok {
			if d, ok2 := dirs[b.section]; ok2 {
				chs := findChapters(d, b.id)
				if len(chs) > maxCh {
					maxCh = len(chs)
				}
				hasAr = len(chs) > 0
			}
		}
		// Hebrew (OT only, optional) — files use Hebrew transliteration names
		heFileID := b.id
		if hid, ok := englishToHebrew[b.id]; ok {
			heFileID = hid
		}
		if b.section != "nt" {
			if dirs, ok := langDirs["he"]; ok {
				if d, ok2 := dirs[b.section]; ok2 {
					chs := findChapters(d, heFileID)
					if len(chs) > maxCh {
						maxCh = len(chs)
					}
					hasHe = len(chs) > 0
				}
			}
		}
		// Syriac + Greek (NT only, optional) — files omit hyphen in numbered books
		ntFileID := b.id
		if gid, ok := englishToGreek[b.id]; ok {
			ntFileID = gid
		}
		if b.section == "nt" {
			if dirs, ok := langDirs["syr"]; ok {
				if d, ok2 := dirs["nt"]; ok2 {
					chs := findChapters(d, ntFileID)
					if len(chs) > maxCh {
						maxCh = len(chs)
					}
					hasSyr = len(chs) > 0
				}
			}
			if dirs, ok := langDirs["el"]; ok {
				if d, ok2 := dirs["nt"]; ok2 {
					chs := findChapters(d, ntFileID)
					if len(chs) > maxCh {
						maxCh = len(chs)
					}
					hasEl = len(chs) > 0
				}
			}
		}

		if maxCh == 0 {
			continue // Book not in any source
		}

		meta := BookMeta{
			ID:          b.id,
			Hebrew:      b.hebrew,
			Convention:  b.conventional,
			Section:     b.section,
			SectionName: b.sectionName,
			Order:       i + 1,
			Chapters:    maxCh,
			HasHebrew:   hasHe,
			HasSyriac:   hasSyr,
			HasGreek:    hasEl,
			HasEnglish:  hasEn,
			HasArabic:   hasAr,
		}
		books = append(books, meta)
	}

	fmt.Fprintf(os.Stderr, "Found %d books\n", len(books))

	// Write books index
	booksJSON, _ := json.MarshalIndent(books, "", "  ")
	booksPath := filepath.Join(*outDir, "data", "bible_books.json")
	os.WriteFile(booksPath, booksJSON, 0644)
	fmt.Fprintf(os.Stderr, "Written %s\n", booksPath)

	// Generate per-book JSON with all chapters and all languages merged
	assetsDir := filepath.Join(*outDir, "assets", "bible")
	os.MkdirAll(assetsDir, 0755)

	for _, book := range books {
		var chapters []ChapterData

		for ch := 1; ch <= book.Chapters; ch++ {
			cd := ChapterData{
				Book:    book.ID,
				Chapter: ch,
			}

			allVerseNums := map[int]bool{}

			// Hebrew — use Hebrew transliteration filename
			heVerses := map[int]string{}
			heFileID := book.ID
			if hid, ok := englishToHebrew[book.ID]; ok {
				heFileID = hid
			}
			if book.HasHebrew {
				sec := book.Section
				if dirs, ok := langDirs["he"]; ok {
					if d, ok2 := dirs[sec]; ok2 {
						path := filepath.Join(d, fmt.Sprintf("%s-%02d.md", heFileID, ch))
						title, subtitle, vlines := parseChapter(path)
						if cd.Title == "" {
							cd.Title = title
							cd.Subtitle = subtitle
						}
						heVerses = parseVerses(vlines)
						for n := range heVerses {
							allVerseNums[n] = true
						}
					}
				}
			}

			// Syriac — numbered books omit hyphen
			syrVerses := map[int]string{}
			ntFileID := book.ID
			if gid, ok := englishToGreek[book.ID]; ok {
				ntFileID = gid
			}
			if book.HasSyriac {
				if dirs, ok := langDirs["syr"]; ok {
					if d, ok2 := dirs["nt"]; ok2 {
						path := filepath.Join(d, fmt.Sprintf("%s-%02d.md", ntFileID, ch))
						title, subtitle, vlines := parseChapter(path)
						if cd.Title == "" {
							cd.Title = title
							cd.Subtitle = subtitle
						}
						syrVerses = parseVerses(vlines)
						for n := range syrVerses {
							allVerseNums[n] = true
						}
					}
				}
			}

			// Greek — same filename convention as Syriac
			elVerses := map[int]string{}
			if book.HasGreek {
				if dirs, ok := langDirs["el"]; ok {
					if d, ok2 := dirs["nt"]; ok2 {
						path := filepath.Join(d, fmt.Sprintf("%s-%02d.md", ntFileID, ch))
						title, subtitle, vlines := parseChapter(path)
						if cd.Title == "" {
							cd.Title = title
							cd.Subtitle = subtitle
						}
						elVerses = parseVerses(vlines)
						for n := range elVerses {
							allVerseNums[n] = true
						}
					}
				}
			}

			// English
			enVerses := map[int]string{}
			if book.HasEnglish {
				sec := book.Section
				if dirs, ok := langDirs["en"]; ok {
					if d, ok2 := dirs[sec]; ok2 {
						path := filepath.Join(d, fmt.Sprintf("%s-%02d.md", book.ID, ch))
						title, subtitle, vlines := parseChapter(path)
						if cd.Title == "" {
							cd.Title = title
							cd.Subtitle = subtitle
						}
						enVerses = parseVerses(vlines)
						for n := range enVerses {
							allVerseNums[n] = true
						}
					}
				}
			}

			// Arabic
			arVerses := map[int]string{}
			if book.HasArabic {
				sec := book.Section
				if dirs, ok := langDirs["ar"]; ok {
					if d, ok2 := dirs[sec]; ok2 {
						path := filepath.Join(d, fmt.Sprintf("%s-%02d.md", book.ID, ch))
						_, _, vlines := parseChapter(path)
						arVerses = parseVerses(vlines)
						for n := range arVerses {
							allVerseNums[n] = true
						}
					}
				}
			}

			// Merge verses
			var nums []int
			for n := range allVerseNums {
				nums = append(nums, n)
			}
			sort.Ints(nums)

			for _, n := range nums {
				v := Verse{Num: n}
				if t, ok := heVerses[n]; ok {
					v.Hebrew = t
				}
				if t, ok := syrVerses[n]; ok {
					v.Syriac = t
				}
				if t, ok := elVerses[n]; ok {
					v.Greek = t
				}
				if t, ok := enVerses[n]; ok {
					v.English = t
				}
				if t, ok := arVerses[n]; ok {
					v.Arabic = t
				}
				cd.Verses = append(cd.Verses, v)
			}

			if len(cd.Verses) > 0 {
				chapters = append(chapters, cd)
			}
		}

		if len(chapters) == 0 {
			continue
		}

		bookData := map[string]interface{}{
			"book":     book,
			"chapters": chapters,
		}
		data, _ := json.Marshal(bookData)
		outPath := filepath.Join(assetsDir, book.ID+".json")
		os.WriteFile(outPath, data, 0644)
		fmt.Fprintf(os.Stderr, "  %s: %d chapters, %d verses\n",
			book.ID, len(chapters),
			func() int {
				n := 0
				for _, c := range chapters {
					n += len(c.Verses)
				}
				return n
			}())
	}

	fmt.Fprintln(os.Stderr, "Done!")
}
