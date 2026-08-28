// scripts/gen_library_data.go — Export library_catalog and reader JSON files
// for devotional-pwa from Dolt and extracted data files.
//
// Usage:
//   go run scripts/gen_library_data.go [--dolt-dir /home/joop/bahaiwritings] [--out-dir ./devotional-pwa]

package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type CatalogEntry struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Lang     string `json:"lang"`
	Category string `json:"cat"`
	Religion string `json:"rel"`
	Words    int    `json:"words"`
	Paras    int    `json:"paras"`
	URL      string `json:"url,omitempty"`
}

type BookData struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Author   string   `json:"author"`
	Lang     string   `json:"lang"`
	Category string   `json:"cat"`
	Religion string   `json:"rel"`
	Paras    []string `json:"paras"`
}

type ExtractedDoc struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	Title      string   `json:"title"`
	Author     string   `json:"author"`
	Religion   string   `json:"religion"`
	Category   string   `json:"category"`
	Paragraphs []string `json:"paragraphs"`
}

func main() {
	doltDir := flag.String("dolt-dir", "/home/joop/bahaiwritings", "Dolt repo path")
	outDir := flag.String("out-dir", "/home/joop/prayermatching/devotional-pwa", "devotional-pwa root path")
	extractedDir := flag.String("extracted-dir", "/home/joop/prayermatching/data/bahairesearch/extracted", "Extracted JSON path")
	flag.Parse()

	t0 := time.Now()
	log.Printf("Starting library data generation...")
	log.Printf("Dolt dir: %s, Out dir: %s", *doltDir, *outDir)

	// 1. Query library_catalog from Dolt
	cmd := exec.Command("dolt", "sql", "-r", "csv", "-q",
		"SELECT book_id, religion, category, author, title, language, word_count, para_count, source_url FROM library_catalog ORDER BY language, religion, title")
	cmd.Dir = *doltDir
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("failed to query library_catalog: %v", err)
	}

	r := csv.NewReader(strings.NewReader(string(out)))
	records, err := r.ReadAll()
	if err != nil {
		log.Fatalf("failed to parse CSV: %v", err)
	}

	var catalog []CatalogEntry
	bookMeta := map[string]CatalogEntry{}

	for i, row := range records {
		if i == 0 || len(row) < 8 {
			continue
		}
		bookID := row[0]
		rel := row[1]
		cat := row[2]
		author := row[3]
		title := row[4]
		lang := row[5]
		words, _ := strconv.Atoi(row[6])
		paras, _ := strconv.Atoi(row[7])
		var sourceURL string
		if len(row) > 8 {
			sourceURL = row[8]
		}

		entry := CatalogEntry{
			ID:       bookID,
			Title:    title,
			Author:   author,
			Lang:     lang,
			Category: cat,
			Religion: rel,
			Words:    words,
			Paras:    paras,
			URL:      sourceURL,
		}
		catalog = append(catalog, entry)
		bookMeta[bookID] = entry
	}

	log.Printf("Loaded %d books from Dolt library_catalog", len(catalog))

	// 2. Write catalog.json to static/data and public/data
	staticDataDir := filepath.Join(*outDir, "static", "data")
	publicDataDir := filepath.Join(*outDir, "public", "data")
	_ = os.MkdirAll(staticDataDir, 0755)
	_ = os.MkdirAll(publicDataDir, 0755)

	catalogBytes, err := json.Marshal(catalog)
	if err != nil {
		log.Fatalf("failed to marshal catalog: %v", err)
	}

	catalogFileStatic := filepath.Join(staticDataDir, "library_catalog.json")
	if err := os.WriteFile(catalogFileStatic, catalogBytes, 0644); err != nil {
		log.Fatalf("failed to write %s: %v", catalogFileStatic, err)
	}
	catalogFilePublic := filepath.Join(publicDataDir, "library_catalog.json")
	_ = os.WriteFile(catalogFilePublic, catalogBytes, 0644)

	log.Printf("Wrote %s (%d bytes)", catalogFileStatic, len(catalogBytes))

	// 3. Export book text JSON files for the reader
	staticBooksDir := filepath.Join(staticDataDir, "library", "books")
	publicBooksDir := filepath.Join(publicDataDir, "library", "books")
	_ = os.MkdirAll(staticBooksDir, 0755)
	_ = os.MkdirAll(publicBooksDir, 0755)

	// Scan extracted directory for JSON files
	log.Printf("Scanning extracted JSON files in %s...", *extractedDir)
	var extractedFiles []string
	_ = filepath.Walk(*extractedDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".json") && !strings.Contains(path, "/raw/") {
			extractedFiles = append(extractedFiles, path)
		}
		return nil
	})
	log.Printf("Found %d extracted JSON files", len(extractedFiles))

	exportedCount := 0
	for _, fpath := range extractedFiles {
		data, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		var doc ExtractedDoc
		if err := json.Unmarshal(data, &doc); err != nil || doc.ID == "" {
			continue
		}

		meta, exists := bookMeta[doc.ID]
		if !exists {
			continue // canonical writing or not in library_catalog
		}

		book := BookData{
			ID:       doc.ID,
			Title:    meta.Title,
			Author:   meta.Author,
			Lang:     meta.Lang,
			Category: meta.Category,
			Religion: meta.Religion,
			Paras:    doc.Paragraphs,
		}

		bBytes, err := json.Marshal(book)
		if err != nil {
			continue
		}

		dstStatic := filepath.Join(staticBooksDir, doc.ID+".json")
		_ = os.WriteFile(dstStatic, bBytes, 0644)
		dstPublic := filepath.Join(publicBooksDir, doc.ID+".json")
		_ = os.WriteFile(dstPublic, bBytes, 0644)

		exportedCount++
	}

	log.Printf("Exported %d reader book files to static/data/library/books/ in %v", exportedCount, time.Since(t0))
}
