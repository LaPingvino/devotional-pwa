// match_days.go — Match Days of Remembrance entries to real Phelps inventory codes
//
// All Days of Remembrance entries currently have phelps='BH09600000'.
// This script:
//   1. Maps English entries (sections 1-45) to their real inventory PINs
//   2. Maps non-English entries to the same PINs by matching tablet names/text patterns
//   3. Generates and applies UPDATE SQL
//
// Usage:
//   go run match_days.go [--dry-run] [--dolt-dir ~/bahaiwritings]

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	dryRun  = flag.Bool("dry-run", false, "Print SQL but don't apply")
	doltDir = flag.String("dolt-dir", os.ExpandEnv("$HOME/bahaiwritings"), "Path to dolt repo")
)

// Section number -> inventory PIN mapping (from manual research)
var sectionToPIN = map[int]string{
	1:  "BH03908", // Naw-Ruz tablet
	2:  "BH02896", // Naw-Ruz day festival
	3:  "BH01001", // Lord of the world, Ruler of the nations
	4:  "BH00769", // God testifieth to the unity
	5:  "BH03245", // Sovereign King, Holy of Holies
	6:  "BH00821", // Divine Springtime
	7:  "BH01459", // Proclaim unto the celestial Concourse
	8:  "BH04569", // Dawn of Ridvan Festival
	9:  "BH00030", // First day Ancient Beauty
	10: "BH00379", // Day-star of words
	11: "BH00598", // O Lord my God, unloose my tongue
	12: "BH01966", // Hur-i-Ujab (Wondrous Maiden)
	13: "BH07443", // Ridvan, Our Lord the Most Merciful
	14: "BH02533", // I beseech Thee by this Day
	15: "BH01673", // Cast radiance of all Thy names
	16: "BH00421", // O concourse of earth and heaven
	17: "BH00889", // Gathered loved ones, Most Great Festival
	18: "BH08842", // Ridvan, shed splendour, All-Merciful
	19: "BH04229", // Ridvan, corner of this prison
	20: "BH08256", // Ridvan, servant extended invitation
	21: "BH02198", // Lawh-i-Ashiq va Mashúq
	22: "BH00296", // Throne of transcendent unity
	23: "BH00334", // Suriy-i-Qalam
	24: "BH11205", // Festival of Ridvan, vernal season
	25: "BH00045", // Another letter, hallowed days of Ridvan
	26: "BH00759", // Lawh-i-Naqus
	27: "BH00729", // Lawh-i-Ghulamul-Khuld
	28: "BH01928", // Tongue of Glory, Word of God
	29: "BH00939", // Suriy-i-Ghusn
	30: "BH01865", // Lawh-i-Rasul
	31: "BH00579", // Lawh-i-Maryam
	32: "BH00003", // Kitab-i-Ahd
	33: "BH02307", // Tablet of Visitation
	34: "BH01079", // Give ear, O My servant
	35: "BH00031", // Suriy-i-Nush excerpt
	36: "BH00021", // Suriy-i-Muluk excerpt
	37: "BH00066", // Lawh-i-Salman I excerpt
	38: "BH00297", // Suriy-i-Dhikr
	39: "BH00155", // Suriy-i-Ahzan excerpt
	40: "BH02162", // Born on this day, Herald
	41: "BH03262", // Adorned world with splendour of dawn
	42: "BH01010", // Lawh-i-Mawlud
	43: "BH02783", // Birthday Festival
	44: "BH01716", // O concourse of ardent lovers
	45: "BH05154", // Month wherein was born
}

// English source_id to section number (days_187=section 1, etc.)
func englishSourceToSection(sid string) int {
	var id int
	fmt.Sscanf(sid, "days_%d", &id)
	if id >= 187 && id <= 231 {
		return id - 186
	}
	return 0
}

// Italian: days_257..days_300 = sections 1..44
func italianSourceToSection(sid string) int {
	var id int
	fmt.Sscanf(sid, "days_%d", &id)
	if id >= 257 && id <= 300 {
		return id - 256
	}
	return 0
}

// Norwegian: days_320..days_364 = sections 1..45
func norwegianSourceToSection(sid string) int {
	var id int
	fmt.Sscanf(sid, "days_%d", &id)
	if id >= 320 && id <= 364 {
		return id - 319
	}
	return 0
}

// For partial languages, match by tablet name patterns or text clues
type sectionMatcher struct {
	section int
	pattern *regexp.Regexp
}

var partialMatchers = []sectionMatcher{
	// Tablet names (work across all languages since they keep Arabic/Persian names)
	{12, regexp.MustCompile(`(?i)Ḥúr-i-['']Ujáb|Ḥúr-i-'Ujáb|Huri.*Ujab|Wondrous Maiden|Ancella Meravigliosa`)},
	{21, regexp.MustCompile(`(?i)Lawḥ-i-['']Áshiq|Ashiq.*Ma.*sh|Lover.*Beloved|Amante.*Amato|Elskeren.*Elskede`)},
	{23, regexp.MustCompile(`(?i)Súriy-i-Qalam|Sura.*Calamo|Pennens sure|Surih of the Pen|S.rih.*Pen`)},
	{26, regexp.MustCompile(`(?i)Lawḥ-i-Náqús|Naqus|Tabla de la Campana|Tavola della Campana|Klokke`)},
	{27, regexp.MustCompile(`(?i)Lawḥ-i-Ghulámu|Ghulámu.*l-Khuld|Immortal Youth|Giovane Immortale|Udødelige`)},
	{29, regexp.MustCompile(`(?i)Súriy-i-Ghuṣn|Tablet of the Branch|Tavola del Ramo|Grenens`)},
	{30, regexp.MustCompile(`(?i)Lawḥ-i-Rasúl|Tabla.*Rasúl|Tavola.*Rasúl|Rasúl`)},
	{31, regexp.MustCompile(`(?i)Lawḥ-i-Maryam|Tabla.*Maryam|Tavola.*Maryam`)},
	{32, regexp.MustCompile(`(?i)Kitáb-i-['']Ahd|Book of the Covenant|Libro del Patto|Paktens bok`)},
	{33, regexp.MustCompile(`(?i)Tablet of Visitation|Tavola della Visitazione|Besøkelsens tavle|Tabla de Visitación`)},
	{35, regexp.MustCompile(`(?i)Súriy-i-Nuṣḥ|Nush|S.rih.*Counsel|Sura del Consiglio|Råd.suren|Maningens sura`)},
	{36, regexp.MustCompile(`(?i)Súriy-i-Mulúk|S.rih.*Kings|Sura dei Re|Kongenes sure`)},
	{37, regexp.MustCompile(`(?i)Lawḥ-i-Salmán|Salm.n I|Tavola.*Salmán|Salmán`)},
	{38, regexp.MustCompile(`(?i)Súriy-i-Dhikr|S.rih.*Remembrance|Sura della Rimembranza|Ihukommelsens sure`)},
	{39, regexp.MustCompile(`(?i)Súriy-i-Aḥzán|Ahzan|S.rih.*Sorrow|Sura dei Dolori|Sorgenes sure`)},
	{42, regexp.MustCompile(`(?i)Lawḥ-i-Mawlúd|Tabla del Natalicio|Tavola della Natività|Fødselstavlen|Födelseskriften|Fødselsskriften|Janana.*Mawlúd`)},

	// Text-based patterns for non-named sections
	// Section 1: Naw-Ruz
	{1, regexp.MustCompile(`(?i)Naw-Rúz|naw.?rúz`)},
	// Section 40: "born on this day...Herald"
	{40, regexp.MustCompile(`(?i)born.*this day.*Herald|nacido.*este día.*Heraldo|nato.*questo giorno.*Araldo|født.*denne dag.*bud|syntyneen.*airueksi|föddes denna dag.*Härolden|ipinanganak|isinilang`)},
	// Section 41: "adorned the world with the splendour of the dawn"
	{41, regexp.MustCompile(`(?i)adorned the world.*splendour.*dawn|adornato il mondo.*fulgida aurora|smyckat världen|adornado el mundo.*esplendor.*amanecer`)},
	// Section 42: "concourse of the seen and the unseen! Rejoice"
	// (handled by Lawh-i-Mawlud above)
	// Section 43: "Birthday Festival is come"
	{43, regexp.MustCompile(`(?i)Birthday Festival is come|Festividad del Natalicio|Festività della Nascita|Fødselsdagens høytid|Födelsedagshögtiden|Syntymäpäiväjuhla`)},
	// Section 44: "concourse of ardent lovers"
	{44, regexp.MustCompile(`(?i)concourse of ardent lovers|concurso de amantes ardientes|accolta di ardenti amanti|glødende elskere|brinnande älskare`)},
	// Section 45: "month wherein was born He Who beareth"
	{45, regexp.MustCompile(`(?i)month wherein was born|mes.*nació.*Portador|mese.*nacque.*Nome|måned.*fødtes|kuu.*syntyi|månad.*föddes`)},
	// Section 2: "ordained this day as a festival"
	{2, regexp.MustCompile(`(?i)ordained this day.*festival|instiftat denna dag.*högtid|säätänyt tämän päivän`)},
	// Section 3: "Lord of the world and the Ruler of the nations"
	{3, regexp.MustCompile(`(?i)Lord of the world.*Ruler of the nations|Signore del mondo.*Re.*nazioni|Herre.*världen|jordens Herre.*nationernas Härskare|verdens Herre.*nasjonenes hersker`)},
	// Section 4: "God testifieth to the unity of His Godhood"
	{4, regexp.MustCompile(`(?i)God testifieth to the unity|Dio attesta l.unità|Gud bevitner.*guddommelighet`)},
	// Section 5: "Sovereign King, the Holy of Holies" or "This is that Day among Thy Days"
	{5, regexp.MustCompile(`(?i)Sovereign King.*Holy of Holies|Re Sovrano.*Santo dei Santi|enerådende.*hellighetenes|Ylivaltainen kuningas`)},
	// Section 6: "Divine Springtime"
	{6, regexp.MustCompile(`(?i)Divine Springtime|Primavera divina|guddommelige forår`)},
	// Section 7: "Proclaim unto the celestial Concourse"
	{7, regexp.MustCompile(`(?i)Proclaim.*celestial Concourse|Forkynn.*himmelske skare|proclama.*Concurso celestial`)},
	// Section 8: "dawn of Thy Ridvan Festival hath broken"
	{8, regexp.MustCompile(`(?i)dawn of Thy Ri.*ván Festival hath broken|alba.*Riḍván|din riḍván-høytid har begynt`)},
	// Section 9: "first day that the Ancient Beauty"
	{9, regexp.MustCompile(`(?i)first day.*Ancient Beauty|primo giorno.*Antica Bellezza|første dag.*urgamle skjønnhet`)},
	// Section 10: "day-star of words, dawning"
	{10, regexp.MustCompile(`(?i)day-?star of words.*dawning|stella mattutina delle parole|Ordenes dagstjerne`)},
	// Section 11: "unloose my tongue to extol"
	{11, regexp.MustCompile(`(?i)unloose my tongue|sciogliere la lingua|løse tungen`)},
	// Section 13: "Our Lord the Most Merciful.*one of the days.*Ridvan"
	{13, regexp.MustCompile(`(?i)Our Lord.*Merciful.*one of the days.*festival.*Ri|nostro Signore.*Misericordiosissimo.*uno dei giorni.*Riḍván|vår Herre.*nåderikeste.*en av dagene.*riḍván`)},
	// Section 14: "I beseech Thee by this Day, and by Him"
	{14, regexp.MustCompile(`(?i)beseech.*by this Day.*Him Whom Thy sovereignty|supplico.*questo Giorno.*sovranità|bønnfaller.*denne dag.*herskermakt`)},
	// Section 15: "cast in this Day the radiance of all Thy names"
	{15, regexp.MustCompile(`(?i)cast in this Day.*radiance.*all Thy names|questa dag.*kastet glansen.*alle dine navn`)},
	// Section 16: "O concourse of earth and heaven"
	{16, regexp.MustCompile(`(?i)concourse of earth and heaven|schiere della terra.*del cielo|jordens og himmelens forsamling`)},
	// Section 17: "gathered.*loved ones.*Most Great Festival"
	{17, regexp.MustCompile(`(?i)gathered.*loved ones.*Most Great Festival|riunito.*amati.*Più Grande Festività|samlet dine elskede.*største høytid`)},
	// Section 18: "Ridvan.*shed.*splendour.*All-Merciful"
	{18, regexp.MustCompile(`(?i)Ri.*ván.*shed.*splendour.*name.*All-Merciful|Riḍván.*effuso.*Misericordiosissimo|riḍván.*kastet glansen.*allbarmhjertige`)},
	// Section 19: "Ridvan.*corner of this prison"
	{19, regexp.MustCompile(`(?i)Ri.*ván.*corner of this prison|Riḍván.*angolo di questa prigione|riḍván.*hjørne.*fengsel`)},
	// Section 20: "Ridvan.*servant.*extended an invitation"
	{20, regexp.MustCompile(`(?i)Ri.*ván.*servant.*invitation|Riḍván.*servo.*invito|riḍván.*tjener.*innbydelse`)},
	// Section 22: "stablished Thyself upon the throne of Thy transcendent"
	{22, regexp.MustCompile(`(?i)stablished.*throne.*transcendent|insediato.*trono.*trascendente|inntatt.*overopphøyede.*enhets trone`)},
	// Section 24: "Festival of Ridvan, the vernal season"
	{24, regexp.MustCompile(`(?i)Festival of Ri.*ván.*vernal season|Festività di Riḍván.*stagione primaverile|riḍván-høytiden.*forårstiden`)},
	// Section 25: "Another letter.*hallowed.*blessed days of Ridvan"
	{25, regexp.MustCompile(`(?i)Another letter.*hallowed.*blessed.*Ri.*ván|Un.altra.*lettera.*santificati.*velsignede|nok et brev.*helligede.*riḍván`)},
	// Section 28: "Tongue of Glory hath called aloud"
	{28, regexp.MustCompile(`(?i)Tongue of Glory.*called aloud|Lingua della Gloria.*parlato|herlighetens tunge.*ropt`)},
	// Section 34: "Give ear, O My servant"
	{34, regexp.MustCompile(`(?i)Give ear.*My servant.*Throne of thy Lord|O Mio servo.*tendi l.orecchio|Lytt.*min tjener.*Herres trone|Lyssna.*Min tjänare.*Herres.*tron`)},
}

// For Swedish partial entries, handle special numbering in text
var svSectionMap = map[string]int{
	"days_238": 40,  // Born on this day
	"days_239": 41,  // Adorned world with dawn
	"days_240": 42,  // Lawh-i-Mawlud
	"days_241": 43,  // Birthday Festival
	"days_242": 44,  // Ardent lovers
	"days_243": 45,  // Month wherein born
	"days_304": 1,   // Naw-Ruz
	"days_305": 2,   // Ordained this day festival
	"days_306": 3,   // Lord of world, Ruler
	"days_307": 5,   // Sovereign King
	"days_308": 28,  // "Babs tillkannagivande" -> section 28 (Tongue of Glory / Declaration of the Bab)
	"days_309": 34,  // Give ear, O My servant
	"days_310": 35,  // Suriy-i-Nush
	"days_311": 37,  // Lawh-i-Salman I
}

// For Spanish partial entries
var esSectionMap = map[string]int{
	"days_244": 40,  // Born on this day
	"days_245": 41,  // Adorned world
	"days_246": 42,  // Lawh-i-Mawlud
	"days_247": 43,  // Birthday Festival
	"days_248": 44,  // Ardent lovers
	"days_249": 45,  // Month wherein born
	"days_250": 7,   // Proclaim unto celestial Concourse
	"days_251": 26,  // Lawh-i-Naqus
	"days_252": 30,  // Lawh-i-Rasul
	"days_253": 39,  // Suriy-i-Ahzan excerpt
}

// For Tagalog partial entries
var tlSectionMap = map[string]int{
	"days_232": 40,  // Born on this day
	"days_233": 41,  // Eternal, the One
	"days_234": 42,  // Lawh-i-Mawlud
	"days_235": 43,  // Birthday Festival
	"days_236": 44,  // Ardent lovers
	"days_237": 45,  // Month wherein born
}

// For Finnish partial entries
var fiSectionMap = map[string]int{
	"days_365": 2,   // "ordained this day as a festival" (Naw-Ruz day 2)
	"days_366": 5,   // Sovereign King
	"days_367": 40,  // Born on this day
	"days_368": 43,  // Birthday Festival
	"days_369": 45,  // Month wherein born
}

// For French partial entries
var frSectionMap = map[string]int{
	"days_301": 43,  // Birthday Festival
	"days_302": 45,  // Month wherein born
	"days_303": 40,  // Born on this day
}

// For Telugu partial entries
var teSectionMap = map[string]int{
	"days_312": 40,  // Born on this day
	"days_313": 41,  // Adorned world
	"days_314": 43,  // Birthday Festival
	"days_315": 45,  // Month wherein born
	"days_316": 42,  // Lawh-i-Mawlud
	"days_317": 44,  // Ardent lovers
	"days_318": 2,   // "ordained this day as festival"
	"days_319": 1,   // Naw-Ruz
}

// For Chinese partial entries
var zhSectionMap = map[string]int{
	"days_254": 45,  // Month wherein born
	"days_255": 43,  // Birthday Festival
	"days_256": 40,  // Born on this day
}

func doltSQL(query string) (string, error) {
	cmd := exec.Command("dolt", "sql", "-q", query, "--result-format", "csv")
	cmd.Dir = *doltDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type entry struct {
	version  string
	sourceID string
	language string
	text     string
}

func main() {
	flag.Parse()

	// Step 1: Query all Days of Remembrance entries
	log.Println("Querying all Days of Remembrance entries...")
	out, err := doltSQL("SELECT version, source_id, language, LEFT(text, 500) FROM writings WHERE type='days_remembrance' ORDER BY language, source_id")
	if err != nil {
		log.Fatalf("Query failed: %v\n%s", err, out)
	}

	var entries []entry
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i, line := range lines {
		if i == 0 {
			continue // skip header
		}
		// CSV parsing - handle quoted fields
		parts := parseCSVLine(line)
		if len(parts) < 4 {
			continue
		}
		entries = append(entries, entry{
			version:  parts[0],
			sourceID: parts[1],
			language: parts[2],
			text:     parts[3],
		})
	}
	log.Printf("Found %d entries", len(entries))

	// Step 2: Map each entry to a section number
	var updates []string
	matched := 0
	unmatched := 0

	for _, e := range entries {
		section := 0

		switch e.language {
		case "en":
			section = englishSourceToSection(e.sourceID)
		case "it":
			section = italianSourceToSection(e.sourceID)
		case "no":
			section = norwegianSourceToSection(e.sourceID)
		case "sv":
			if s, ok := svSectionMap[e.sourceID]; ok {
				section = s
			}
		case "es":
			if s, ok := esSectionMap[e.sourceID]; ok {
				section = s
			}
		case "tl":
			if s, ok := tlSectionMap[e.sourceID]; ok {
				section = s
			}
		case "fi":
			if s, ok := fiSectionMap[e.sourceID]; ok {
				section = s
			}
		case "fr":
			if s, ok := frSectionMap[e.sourceID]; ok {
				section = s
			}
		case "te":
			if s, ok := teSectionMap[e.sourceID]; ok {
				section = s
			}
		case "zh-Hans":
			if s, ok := zhSectionMap[e.sourceID]; ok {
				section = s
			}
		}

		// If not matched by language-specific map, try pattern matching on text
		if section == 0 {
			for _, m := range partialMatchers {
				if m.pattern.MatchString(e.text) {
					section = m.section
					break
				}
			}
		}

		if section == 0 {
			log.Printf("UNMATCHED: %s %s (%s) text=%s", e.language, e.sourceID, e.version, truncate(e.text, 80))
			unmatched++
			continue
		}

		pin, ok := sectionToPIN[section]
		if !ok {
			log.Printf("NO PIN for section %d: %s %s", section, e.language, e.sourceID)
			unmatched++
			continue
		}

		updates = append(updates, fmt.Sprintf("UPDATE writings SET phelps='%s' WHERE version='%s';", pin, e.version))
		matched++
	}

	log.Printf("Matched: %d, Unmatched: %d", matched, unmatched)

	// Step 3: Write SQL file
	sqlContent := "SET FOREIGN_KEY_CHECKS=0;\n" + strings.Join(updates, "\n") + "\nSET FOREIGN_KEY_CHECKS=1;\n"

	tmpDir := os.Getenv("TMPDIR")
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	sqlPath := tmpDir + "/days_remap.sql"
	if err := os.WriteFile(sqlPath, []byte(sqlContent), 0644); err != nil {
		log.Fatalf("Failed to write SQL: %v", err)
	}
	log.Printf("Wrote %d updates to %s", len(updates), sqlPath)

	if *dryRun {
		fmt.Println(sqlContent)
		return
	}

	// Step 4: Apply SQL
	log.Println("Applying SQL...")
	// Use grep to extract SET and UPDATE lines, pipe to dolt sql
	cmd := exec.Command("bash", "-c",
		fmt.Sprintf(`grep '^SET\|^UPDATE' "%s" | dolt sql`, sqlPath))
	cmd.Dir = *doltDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("SQL apply failed: %v", err)
	}
	log.Println("Done! SQL applied successfully.")

	// Verify
	log.Println("Verifying...")
	vout, err := doltSQL("SELECT COUNT(*) as remaining FROM writings WHERE type='days_remembrance' AND phelps='BH09600000'")
	if err == nil {
		log.Printf("Remaining BH09600000 entries: %s", strings.TrimSpace(vout))
	}
}

func truncate(s string, n int) string {
	// Strip HTML tags for readability
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, "")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// Simple CSV line parser that handles quoted fields
func parseCSVLine(line string) []string {
	var fields []string
	var current strings.Builder
	inQuotes := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '"' {
			if inQuotes && i+1 < len(line) && line[i+1] == '"' {
				current.WriteByte('"')
				i++
			} else {
				inQuotes = !inQuotes
			}
		} else if ch == ',' && !inQuotes {
			fields = append(fields, current.String())
			current.Reset()
		} else {
			current.WriteByte(ch)
		}
	}
	fields = append(fields, current.String())
	return fields
}
