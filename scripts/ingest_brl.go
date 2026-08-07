// ingest_brl fetches the text of catalogued book items we hold no text for.
//
// The inventory records, per PIN, deep links into the Bahá'í Reference Library
// (bahai.org/r/<anchor>). Each such link resolves to a chapter page with the
// item's own anchor in it, so one fetch yields exactly one item — original or
// translation, depending on which edition the link points into.
//
// It emits SQL and a TSV report; it never writes to the database. Review the
// report, then apply with:  grep '^INSERT' out.sql | dolt sql
//
//	go run scripts/ingest_brl.go --dolt-dir ~/bahaiwritings --out /tmp/brl.sql
//
// Scope: rows of writing_collections whose phelps has no writings row at all.
// Measure coverage before ingesting — that is how this list was bounded.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	doltDir = flag.String("dolt-dir", os.Getenv("HOME")+"/bahaiwritings", "Dolt repo path")
	outFile = flag.String("out", "/tmp/brl_ingest.sql", "SQL output path")
	tsvFile = flag.String("tsv", "", "optional TSV report path (defaults to --out with .tsv)")
	delay   = flag.Duration("delay", 3*time.Second, "pause between fetches — be polite")
	limit   = flag.Int("limit", 0, "stop after N fetches (0 = all); use for a dry run")
	ua      = flag.String("ua", "holywritings-catalog/1.0 (+https://holywritings.net)", "User-Agent")
)

// The library wraps each numbered item in its own section div; the /r/ anchor
// sits inside the one we want.
var (
	sectionRe = regexp.MustCompile(`(?i)<div[^>]*data-unit="section"`)
	anchorRe  = regexp.MustCompile(`/r/(\d+)`)
	pnumRe    = regexp.MustCompile(`(?is)<a\b[^>]*class="brl-pnum[^"]*"[^>]*>.*?</a>`)
	headRe    = regexp.MustCompile(`(?is)<p[^>]*brl-head[^>]*>.*?</p>`)
	// The signature line ("—'Abdu'l-Bahá") is attribution, not text; a rubric
	// of a few words is enough to flip an extent verdict at prayer length.
	attribRe  = regexp.MustCompile(`(?is)<p[^>]*brl-align-right[^>]*>.*?</p>`)
	scriptRe  = regexp.MustCompile(`(?is)<script.*?</script>|<style.*?</style>`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
	wsRe      = regexp.MustCompile(`\s+`)
	itemNumRe = regexp.MustCompile(`[–-]\s*(\d+)\s*[–-]`)
	langRe    = regexp.MustCompile(`^https?://[^/]+/([a-z]{2}(?:-[A-Za-z]+)?)/library/`)
)

type job struct {
	Phelps, Key, Position, RefSource, URL string
	IsExcerpt                             bool
}

type result struct {
	Job                            job
	Lang, Num, Text, Anchor, Final string
}

func main() {
	flag.Parse()
	if *tsvFile == "" {
		*tsvFile = strings.TrimSuffix(*outFile, ".sql") + ".tsv"
	}

	jobs := workList()
	log.Printf("%d fetches across %d PINs", len(jobs), countPINs(jobs))

	sql, err := os.Create(*outFile)
	must(err)
	defer sql.Close()
	tsv, err := os.Create(*tsvFile)
	must(err)
	defer tsv.Close()
	fmt.Fprintf(tsv, "phelps\tcollection\tposition\tlang\titem\twords\tinv_words\tratio\tnote\tfinal_url\topening\n")
	fmt.Fprintf(sql, "-- ingest_brl %s — review before applying\n", time.Now().UTC().Format(time.RFC3339))

	invWords := inventoryWords()
	client := &http.Client{Timeout: 45 * time.Second}
	best := map[string]result{}
	var ok, empty, failed int
	for i, j := range jobs {
		if *limit > 0 && i >= *limit {
			log.Printf("--limit %d reached", *limit)
			break
		}
		if i > 0 {
			time.Sleep(*delay)
		}
		anchor := anchorRe.FindStringSubmatch(j.URL)
		if anchor == nil {
			failed++
			continue
		}
		page, final, err := fetch(client, j.URL)
		if err != nil {
			log.Printf("  ! %s %s: %v", j.Phelps, j.URL, err)
			failed++
			continue
		}
		num, text := itemText(page, anchor[1])
		// A long tablet is paginated as one page of many sections, and the
		// anchor points at a paragraph inside it — so the section walk returns
		// only that paragraph. When the result is a small fraction of what the
		// catalogue says the work is, and the page holds many sections, the
		// page IS the work: take all of it.
		if inv := invWords[j.Phelps]; inv > 0 && len(strings.Fields(text)) < inv/4 {
			if whole := wholeWork(page); len(strings.Fields(whole)) > len(strings.Fields(text)) {
				text = whole
			}
		}
		if text == "" {
			log.Printf("  ~ %s: anchor %s not found in %s", j.Phelps, anchor[1], final)
			empty++
			continue
		}
		// A PIN often carries several anchors into the same item (one at the
		// item head, others at inner paragraphs). Keep the fullest extraction
		// per PIN+language rather than emitting near-duplicate rows.
		k := j.Phelps + "\x00" + langOf(final)
		if prev, seen := best[k]; !seen || len(text) > len(prev.Text) {
			best[k] = result{j, langOf(final), num, text, anchor[1], final}
		}
		ok++
		if ok%25 == 0 {
			log.Printf("  %d fetched (%d empty, %d failed)", ok, empty, failed)
		}
	}

	// Gate on extent. A fallback extraction can swallow a whole chapter when a
	// page has no section wrapper, which looks like a successful fetch and is
	// the worst failure mode here — so score every row against the inventory's
	// own word count and refuse to emit a bare INSERT for an implausible one.
	// Flagged rows are still written, commented, so nothing is lost silently
	// and the repo's apply idiom (grep '^INSERT' | dolt sql) skips them.
	var clean, flagged int
	for _, r := range best {
		words := len(strings.Fields(r.Text))
		inv := invWords[r.Job.Phelps]
		ratio, note := 0.0, ""
		if inv > 0 {
			ratio = float64(words) / float64(inv)
			switch {
			case ratio > 3:
				note = "too long — probably swallowed neighbouring items"
			case ratio < 0.25 && !r.Job.IsExcerpt:
				// Only meaningful for whole items: an excerpt is measured
				// against its whole tablet's count, so a low ratio is the
				// expected reading, not a defect.
				note = "too short — probably a fragment"
			}
		} else {
			note = "no inventory word count to check against"
		}
		fmt.Fprintf(tsv, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%.2f\t%s\t%s\t%s\n",
			r.Job.Phelps, r.Job.Key, r.Job.Position, r.Lang, r.Num, words, inv, ratio,
			note, r.Final, firstWords(r.Text, 12))
		stmt := fmt.Sprintf("INSERT INTO writings (phelps, language, name, type, text, source, source_id, link) VALUES (%s, %s, %s, 'prayer', %s, 'bahai.org', %s, %s);",
			q(r.Job.Phelps), q(r.Lang), q(strings.TrimSpace(strings.ToUpper(r.Job.RefSource)+" "+r.Num)),
			q(r.Text), q(r.Anchor), q(r.Final))
		if note == "" {
			fmt.Fprintln(sql, stmt)
			clean++
		} else {
			fmt.Fprintf(sql, "-- REVIEW (%s, %d words vs inventory %d): %s\n", note, words, inv, stmt)
			flagged++
		}
	}
	log.Printf("extent gate: %d rows pass, %d held for review", clean, flagged)
	log.Printf("done: %d fetched -> %d rows after dedup, %d anchors not found, %d failures",
		ok, len(best), empty, failed)
	log.Printf("SQL: %s\nTSV: %s", *outFile, *tsvFile)
	log.Printf("Review the TSV — check word counts against the inventory before applying.")
}

// workList asks Dolt which catalogued items have no text at all, and which
// authoritative deep links exist for them. One row per (PIN, url).
func workList() []job {
	rows := doltQuery(`
		SELECT DISTINCT wc.phelps, wc.collection_key, wc.position, r.source, r.url, wc.is_excerpt
		FROM writing_collections wc
		JOIN inventory_refs r ON r.PIN = wc.phelps
		WHERE NOT EXISTS (SELECT 1 FROM writings w WHERE w.phelps = wc.phelps)
		  AND r.url REGEXP '^https?://(www\\.)?bahai\\.org/r/[0-9]+$'
		ORDER BY wc.collection_key, wc.position`)
	var out []job
	for _, r := range rows[1:] {
		if len(r) < 6 {
			continue
		}
		out = append(out, job{Phelps: r[0], Key: r[1], Position: r[2], RefSource: r[3], URL: r[4],
			IsExcerpt: r[5] == "1" || strings.EqualFold(r[5], "true")})
	}
	return out
}

// itemText returns the item number and plain text of the section holding anchor.
func itemText(page, anchor string) (string, string) {
	at := strings.Index(page, `id="`+anchor+`"`)
	if at < 0 {
		return "", ""
	}
	var start, end = -1, len(page)
	for _, m := range sectionRe.FindAllStringIndex(page, -1) {
		if m[0] <= at {
			start = m[0]
		} else {
			end = m[0]
			break
		}
	}
	if start < 0 {
		// Single-item pages carry no section wrapper — the whole document is
		// the item. Start at the paragraph the anchor sits in (starting at the
		// anchor itself would cut mid-tag and leak the tag's remainder into the
		// text) and stop at the first thing that is certainly page furniture.
		start = at
		if p := strings.LastIndex(page[:at], "<p"); p >= 0 {
			start = p
		}
		end = len(page)
		for _, marker := range []string{"inline-note-container", "js-selection-menu", "</main", "<footer"} {
			if i := strings.Index(page[at:], marker); i >= 0 && at+i < end {
				end = at + i
			}
		}
	}
	seg := page[start:end]

	num := ""
	if m := itemNumRe.FindStringSubmatch(seg); m != nil {
		num = m[1]
	}
	seg = pnumRe.ReplaceAllString(seg, " ")
	seg = headRe.ReplaceAllString(seg, " ")   // the "– N –" heading is apparatus
	seg = attribRe.ReplaceAllString(seg, " ") // and so is the signature line
	seg = scriptRe.ReplaceAllString(seg, " ")
	return num, clean(seg)
}

// inventoryWords is the yardstick for the extent gate: the catalogue's own
// word count per PIN. Rounded and occasionally absurd, so it bounds rather
// than decides — see docs/editorial-method.md.
func inventoryWords() map[string]int {
	out := map[string]int{}
	for _, r := range doltQuery("SELECT PIN, `Word count` FROM inventory WHERE `Word count` > 0")[1:] {
		if len(r) < 2 {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(r[1])); err == nil {
			out[r[0]] = n
		}
	}
	return out
}

// wholeWork concatenates every section on the page. BRL pages have no single
// content wrapper, but the sections themselves are the text, so joining them
// yields the work without the navigation furniture around it.
func wholeWork(page string) string {
	idx := sectionRe.FindAllStringIndex(page, -1)
	if len(idx) < 4 {
		return ""
	}
	var b strings.Builder
	for i, m := range idx {
		end := len(page)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		seg := page[m[0]:end]
		seg = pnumRe.ReplaceAllString(seg, " ")
		seg = attribRe.ReplaceAllString(seg, " ")
		seg = scriptRe.ReplaceAllString(seg, " ")
		b.WriteString(seg)
		b.WriteString(" ")
	}
	return clean(b.String())
}

// clean turns a markup fragment into text. Order matters: unescape FIRST, then
// strip — stripping first lets escaped markup (&lt;div …) survive and become
// literal markup once unescaped, which leaked "<div class=\"" into 122 rows on
// 2026-08-06. Stripping twice around the unescape makes that impossible.
func clean(seg string) string {
	seg = tagRe.ReplaceAllString(seg, " ")
	seg = html.UnescapeString(seg)
	seg = tagRe.ReplaceAllString(seg, " ")
	for _, ui := range []string{"ادامۀ مطالعه", "Continue reading"} {
		seg = strings.ReplaceAll(seg, ui, " ")
	}
	return strings.TrimSpace(wsRe.ReplaceAllString(seg, " "))
}

// langOf reads the language from the resolved path: bahai.org serves English
// at /library/… and every translation under /<lang>/library/….
func langOf(final string) string {
	if m := langRe.FindStringSubmatch(final); m != nil {
		return m[1]
	}
	return "en"
}

func fetch(c *http.Client, url string) (string, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", *ua)
	resp, err := c.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", "", err
	}
	return string(body), resp.Request.URL.String(), nil
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

func countPINs(j []job) int {
	s := map[string]bool{}
	for _, x := range j {
		s[x.Phelps] = true
	}
	return len(s)
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

func q(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
