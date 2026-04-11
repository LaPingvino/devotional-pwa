// split_esw.go — Split Epistle to the Son of the Wolf (BH00005) into per-paragraph entries.
//
// Usage:
//   cd ~/bahaiwritings
//   go run ~/prayermatching/scripts/split_esw.go              # dry-run: prints SQL
//   go run ~/prayermatching/scripts/split_esw.go --apply      # applies SQL via dolt sql
package main

import (
	"crypto/rand"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
)

func main() {
	apply := flag.Bool("apply", false, "Apply the SQL via dolt sql (default: dry-run)")
	flag.Parse()

	doltDir := os.Getenv("HOME") + "/bahaiwritings"

	type entry struct {
		lang     string
		source   string
		sourceID string
		origType string
	}

	// Fetch both entries
	entries := []entry{
		{lang: "en", source: "bahaiprayers.net", sourceID: "13691", origType: "lawh"},
		{lang: "fa", source: "bahai.org", sourceID: "esw_fa", origType: "lawh"},
	}

	type langData struct {
		entry      entry
		paragraphs []string
	}

	var allData []langData

	for _, e := range entries {
		q := fmt.Sprintf(`SELECT text FROM writings WHERE phelps='BH00005' AND language='%s'`, e.lang)
		cmd := exec.Command("dolt", "sql", "-r", "csv", "-q", q)
		cmd.Dir = doltDir
		out, err := cmd.Output()
		if err != nil {
			log.Fatalf("dolt sql failed for %s: %v", e.lang, err)
		}

		r := csv.NewReader(strings.NewReader(string(out)))
		r.LazyQuotes = true
		// Read header
		_, err = r.Read()
		if err != nil {
			log.Fatalf("csv header read failed for %s: %v", e.lang, err)
		}
		row, err := r.Read()
		if err != nil {
			log.Fatalf("csv data read failed for %s: %v", e.lang, err)
		}
		text := row[0]

		// Split on double newlines
		rawParas := strings.Split(text, "\n\n")

		// Trim and filter empty
		var paras []string
		for _, p := range rawParas {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				paras = append(paras, trimmed)
			}
		}

		fmt.Fprintf(os.Stderr, "[%s] Total non-empty paragraphs before skipping: %d\n", e.lang, len(paras))
		if len(paras) > 2 {
			fmt.Fprintf(os.Stderr, "[%s] Skipping para 1: %s\n", e.lang, truncate(paras[0], 80))
			fmt.Fprintf(os.Stderr, "[%s] Skipping para 2: %s\n", e.lang, truncate(paras[1], 80))
		}

		// Skip first 2 paragraphs (title lines)
		if len(paras) > 2 {
			paras = paras[2:]
		}

		fmt.Fprintf(os.Stderr, "[%s] Paragraphs to insert: %d\n", e.lang, len(paras))
		if len(paras) > 0 {
			fmt.Fprintf(os.Stderr, "[%s] First paragraph: %s\n", e.lang, truncate(paras[0], 120))
			fmt.Fprintf(os.Stderr, "[%s] Last paragraph:  %s\n", e.lang, truncate(paras[len(paras)-1], 120))
		}

		allData = append(allData, langData{entry: e, paragraphs: paras})
	}

	// Check paragraph count match
	if len(allData) == 2 {
		enCount := len(allData[0].paragraphs)
		faCount := len(allData[1].paragraphs)
		if enCount != faCount {
			fmt.Fprintf(os.Stderr, "\nWARNING: paragraph count mismatch! en=%d, fa=%d\n", enCount, faCount)
			fmt.Fprintf(os.Stderr, "The texts have different structures. Review before applying.\n")
		} else {
			fmt.Fprintf(os.Stderr, "\nParagraph counts match: %d paragraphs each.\n", enCount)
		}
	}

	// Generate SQL
	var sql strings.Builder
	sql.WriteString("SET FOREIGN_KEY_CHECKS=0;\n\n")

	// Delete originals
	for _, d := range allData {
		sql.WriteString(fmt.Sprintf(
			"DELETE FROM writings WHERE phelps='BH00005' AND language='%s' AND source='%s';\n",
			d.entry.lang, d.entry.source))
	}
	sql.WriteString("\n")

	// Insert paragraph entries
	for _, d := range allData {
		for i, para := range d.paragraphs {
			phelps := fmt.Sprintf("BH00005%03d", i+1)
			uuid := generateUUID()
			escapedText := escapeSQLString("<p>" + para + "</p>")

			// Name: "Epistle to the Son of the Wolf §N"
			var nameStr string
			if d.entry.lang == "en" {
				nameStr = fmt.Sprintf("'Epistle to the Son of the Wolf §%d'", i+1)
			} else if d.entry.lang == "fa" {
				nameStr = fmt.Sprintf("'لوح ابن ذئب §%d'", i+1)
			}

			sql.WriteString(fmt.Sprintf(
				"INSERT INTO writings (version, phelps, language, source, source_id, type, name, text, is_verified) "+
					"VALUES ('%s', '%s', '%s', '%s', '%s', '%s', %s, '%s', 1);\n",
				uuid, phelps, d.entry.lang, d.entry.source, d.entry.sourceID,
				d.entry.origType, nameStr, escapedText))
		}
		sql.WriteString("\n")
	}

	sql.WriteString("SET FOREIGN_KEY_CHECKS=1;\n")

	sqlStr := sql.String()

	if *apply {
		fmt.Fprintf(os.Stderr, "\nApplying SQL via dolt sql...\n")
		cmd := exec.Command("dolt", "sql")
		cmd.Dir = doltDir
		cmd.Stdin = strings.NewReader(sqlStr)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("dolt sql failed: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Done. Run: cd %s && dolt diff\n", doltDir)
	} else {
		fmt.Fprintf(os.Stderr, "\n--- DRY RUN: SQL output follows ---\n\n")
		io.Copy(os.Stdout, strings.NewReader(sqlStr))
		fmt.Fprintf(os.Stderr, "\nTo apply, run with --apply\n")
	}
}

func truncate(s string, n int) string {
	// Replace newlines for display
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func escapeSQLString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `''`)
	return s
}

func generateUUID() string {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		log.Fatalf("crypto/rand failed: %v", err)
	}
	// Set version 4 and variant bits
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
