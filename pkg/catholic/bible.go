package catholic

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BookDefinition maps a Bible book to its file path, BibleGet query code, and display name.
type BookDefinition struct {
	FilePath     string // e.g. "novotestamento/jo.json"
	BibleGetCode string // e.g. "Jn"
	DisplayName  string // e.g. "São João"
}

// In-memory cache for downloaded Portuguese Catholic books to achieve instant <1ms response.
var (
	bookCache sync.Map
	reRef     = regexp.MustCompile(`(?i)(?:^|versiculo\s+|biblia\s+|passagem\s+)?([1-3]?\s*[a-zá-úç]+)\s*(\d+)[\s:,]+(\d+)(?:\s*[-–]\s*(\d+))?`)
)

type PtBookData struct {
	Livro     string `json:"livro"`
	Capitulos []struct {
		Capitulo   int `json:"capitulo"`
		Versiculos []struct {
			Numero int    `json:"numero"`
			Texto  string `json:"texto"`
		} `json:"versiculos"`
	} `json:"capitulos"`
}

type BibleGetResponse struct {
	Results []struct {
		Book    string `json:"book"`
		Chapter int    `json:"chapter"`
		Verse   string `json:"verse"`
		Text    string `json:"text"`
		Version string `json:"version"`
	} `json:"results"`
	Errors []interface{} `json:"errors"`
}

type BibleAPIResponse struct {
	Reference string `json:"reference"`
	Text      string `json:"text"`
	Verses    []struct {
		Verse int    `json:"verse"`
		Text  string `json:"text"`
	} `json:"verses"`
}

// Curated pool of classic Catholic verses for daily/spontaneous requests
var dailyCatholicVerses = []string{
	"João 3:16",
	"Salmos 23:1",
	"Filipenses 4:13",
	"Romanos 8:28",
	"Mateus 6:33",
	"1 Coríntios 13:4-7",
	"Salmos 91:1-2",
	"Jeremias 29:11",
	"Isaías 41:10",
	"Provérbios 3:5-6",
	"Mateus 11:28",
	"Sabedoria 3:1",
	"Tobias 4:7",
	"Eclesiástico 2:1",
	"Lucas 1:37",
	"João 14:6",
	"Gálatas 2:20",
	"Hebreus 11:1",
	"Tiago 1:5",
	"Apocalipse 21:4",
}

var bookRegistry = map[string]BookDefinition{
	// Pentateuco
	"gn":           {"antigotestamento/gn.json", "Gn", "Gênesis"},
	"genesis":      {"antigotestamento/gn.json", "Gn", "Gênesis"},
	"ex":           {"antigotestamento/ex.json", "Ex", "Êxodo"},
	"exodo":        {"antigotestamento/ex.json", "Ex", "Êxodo"},
	"lv":           {"antigotestamento/lv.json", "Lv", "Levítico"},
	"levitico":     {"antigotestamento/lv.json", "Lv", "Levítico"},
	"nm":           {"antigotestamento/nm.json", "Nm", "Números"},
	"numeros":      {"antigotestamento/nm.json", "Nm", "Números"},
	"dt":           {"antigotestamento/dt.json", "Dt", "Deuteronômio"},
	"deuteronomio": {"antigotestamento/dt.json", "Dt", "Deuteronômio"},

	// Históricos
	"js":              {"antigotestamento/js.json", "Jos", "Josué"},
	"josue":           {"antigotestamento/js.json", "Jos", "Josué"},
	"ju":              {"antigotestamento/ju.json", "Jdc", "Juízes"},
	"jz":              {"antigotestamento/ju.json", "Jdc", "Juízes"},
	"juizes":          {"antigotestamento/ju.json", "Jdc", "Juízes"},
	"rt":              {"antigotestamento/rt.json", "Rt", "Rute"},
	"rute":            {"antigotestamento/rt.json", "Rt", "Rute"},
	"1sm":             {"antigotestamento/1sm.json", "1Sam", "1 Samuel"},
	"1samuel":         {"antigotestamento/1sm.json", "1Sam", "1 Samuel"},
	"2sm":             {"antigotestamento/2sm.json", "2Sam", "2 Samuel"},
	"2samuel":         {"antigotestamento/2sm.json", "2Sam", "2 Samuel"},
	"1rs":             {"antigotestamento/1rs.json", "1Reg", "1 Reis"},
	"1reis":           {"antigotestamento/1rs.json", "1Reg", "1 Reis"},
	"2rs":             {"antigotestamento/2rs.json", "2Reg", "2 Reis"},
	"2reis":           {"antigotestamento/2rs.json", "2Reg", "2 Reis"},
	"1pa":             {"antigotestamento/1pa.json", "1Par", "1 Crônicas"},
	"1cronicas":       {"antigotestamento/1pa.json", "1Par", "1 Crônicas"},
	"2pa":             {"antigotestamento/2pa.json", "2Par", "2 Crônicas"},
	"2cronicas":       {"antigotestamento/2pa.json", "2Par", "2 Crônicas"},
	"esd":             {"antigotestamento/esd.json", "Esr", "Esdras"},
	"esdras":          {"antigotestamento/esd.json", "Esr", "Esdras"},
	"ne":              {"antigotestamento/ne.json", "Neh", "Neemias"},
	"neemias":         {"antigotestamento/ne.json", "Neh", "Neemias"},
	"tob":             {"antigotestamento/tob.json", "Tob", "Tobias"},
	"tb":              {"antigotestamento/tob.json", "Tob", "Tobias"},
	"tobias":          {"antigotestamento/tob.json", "Tob", "Tobias"},
	"jdi":             {"antigotestamento/jdi.json", "Idt", "Judite"},
	"judite":          {"antigotestamento/jdi.json", "Idt", "Judite"},
	"est":             {"antigotestamento/est.json", "Est", "Ester"},
	"ester":           {"antigotestamento/est.json", "Est", "Ester"},
	"1ma":             {"antigotestamento/1ma.json", "1Macc", "1 Macabeus"},
	"1mc":             {"antigotestamento/1ma.json", "1Macc", "1 Macabeus"},
	"1macabeus":       {"antigotestamento/1ma.json", "1Macc", "1 Macabeus"},
	"2ma":             {"antigotestamento/2ma.json", "2Macc", "2 Macabeus"},
	"2mc":             {"antigotestamento/2ma.json", "2Macc", "2 Macabeus"},
	"2macabeus":       {"antigotestamento/2ma.json", "2Macc", "2 Macabeus"},

	// Sapienciais
	"job":          {"antigotestamento/job.json", "Iob", "Jó"},
	"ps":           {"antigotestamento/ps.json", "Ps", "Salmos"},
	"sl":           {"antigotestamento/ps.json", "Ps", "Salmos"},
	"salmo":        {"antigotestamento/ps.json", "Ps", "Salmos"},
	"salmos":       {"antigotestamento/ps.json", "Ps", "Salmos"},
	"pv":           {"antigotestamento/pv.json", "Prv", "Provérbios"},
	"pr":           {"antigotestamento/pv.json", "Prv", "Provérbios"},
	"proverbios":   {"antigotestamento/pv.json", "Prv", "Provérbios"},
	"ees":          {"antigotestamento/ees.json", "Qoh", "Eclesiastes"},
	"ec":           {"antigotestamento/ees.json", "Qoh", "Eclesiastes"},
	"eclesiastes":  {"antigotestamento/ees.json", "Qoh", "Eclesiastes"},
	"cc":           {"antigotestamento/cc.json", "Ct", "Cântico dos Cânticos"},
	"ct":           {"antigotestamento/cc.json", "Ct", "Cântico dos Cânticos"},
	"canticos":     {"antigotestamento/cc.json", "Ct", "Cântico dos Cânticos"},
	"sa":           {"antigotestamento/sa.json", "Sap", "Sabedoria"},
	"sb":           {"antigotestamento/sa.json", "Sap", "Sabedoria"},
	"sabedoria":    {"antigotestamento/sa.json", "Sap", "Sabedoria"},
	"eus":          {"antigotestamento/eus.json", "Sir", "Eclesiástico (Sirácida)"},
	"sir":          {"antigotestamento/eus.json", "Sir", "Eclesiástico (Sirácida)"},
	"eclo":         {"antigotestamento/eus.json", "Sir", "Eclesiástico (Sirácida)"},
	"eclesiastico": {"antigotestamento/eus.json", "Sir", "Eclesiástico (Sirácida)"},
	"siracida":     {"antigotestamento/eus.json", "Sir", "Eclesiástico (Sirácida)"},

	// Proféticos
	"is":          {"antigotestamento/is.json", "Is", "Isaías"},
	"isaias":      {"antigotestamento/is.json", "Is", "Isaías"},
	"je":          {"antigotestamento/je.json", "Ier", "Jeremias"},
	"jr":          {"antigotestamento/je.json", "Ier", "Jeremias"},
	"jeremias":    {"antigotestamento/je.json", "Ier", "Jeremias"},
	"lm":          {"antigotestamento/lm.json", "Lam", "Lamentações"},
	"lamentacoes": {"antigotestamento/lm.json", "Lam", "Lamentações"},
	"ba":          {"antigotestamento/ba.json", "Bar", "Baruc"},
	"baruc":       {"antigotestamento/ba.json", "Bar", "Baruc"},
	"ez":          {"antigotestamento/ez.json", "Ez", "Ezequiel"},
	"ezequiel":    {"antigotestamento/ez.json", "Ez", "Ezequiel"},
	"dn":          {"antigotestamento/dn.json", "Dan", "Daniel"},
	"daniel":      {"antigotestamento/dn.json", "Dan", "Daniel"},
	"os":          {"antigotestamento/os.json", "Os", "Oseias"},
	"oseias":      {"antigotestamento/os.json", "Os", "Oseias"},
	"jl":          {"antigotestamento/jl.json", "Ioel", "Joel"},
	"joel":        {"antigotestamento/jl.json", "Ioel", "Joel"},
	"am":          {"antigotestamento/am.json", "Am", "Amós"},
	"amos":        {"antigotestamento/am.json", "Am", "Amós"},
	"ab":          {"antigotestamento/ab.json", "Abd", "Abdias"},
	"abdias":      {"antigotestamento/ab.json", "Abd", "Abdias"},
	"jn":          {"antigotestamento/jn.json", "Ion", "Jonas"},
	"jonas":       {"antigotestamento/jn.json", "Ion", "Jonas"},
	"mic":         {"antigotestamento/mic.json", "Mich", "Miqueias"},
	"mq":          {"antigotestamento/mic.json", "Mich", "Miqueias"},
	"miqueias":    {"antigotestamento/mic.json", "Mich", "Miqueias"},
	"na":          {"antigotestamento/na.json", "Nah", "Naum"},
	"naum":        {"antigotestamento/na.json", "Nah", "Naum"},
	"hc":          {"antigotestamento/hc.json", "Hab", "Habacuc"},
	"habacuc":     {"antigotestamento/hc.json", "Hab", "Habacuc"},
	"so":          {"antigotestamento/so.json", "Soph", "Sofonias"},
	"sf":          {"antigotestamento/so.json", "Soph", "Sofonias"},
	"sofonias":    {"antigotestamento/so.json", "Soph", "Sofonias"},
	"ag":          {"antigotestamento/ag.json", "Agg", "Ageu"},
	"ageu":        {"antigotestamento/ag.json", "Agg", "Ageu"},
	"zc":          {"antigotestamento/zc.json", "Zach", "Zacarias"},
	"zacarias":    {"antigotestamento/zc.json", "Zach", "Zacarias"},
	"ml":          {"antigotestamento/ml.json", "Mal", "Malaquias"},
	"malaquias":   {"antigotestamento/ml.json", "Mal", "Malaquias"},

	// Novo Testamento
	"mt":               {"novotestamento/mt.json", "Mt", "São Mateus"},
	"mateus":           {"novotestamento/mt.json", "Mt", "São Mateus"},
	"mc":               {"novotestamento/mc.json", "Mc", "São Marcos"},
	"marcos":           {"novotestamento/mc.json", "Mc", "São Marcos"},
	"lc":               {"novotestamento/lc.json", "Lc", "São Lucas"},
	"lucas":            {"novotestamento/lc.json", "Lc", "São Lucas"},
	"jo":               {"novotestamento/jo.json", "Jn", "São João"},
	"joao":             {"novotestamento/jo.json", "Jn", "São João"},
	"act":              {"novotestamento/act.json", "Act", "Atos dos Apóstolos"},
	"at":               {"novotestamento/act.json", "Act", "Atos dos Apóstolos"},
	"atos":             {"novotestamento/act.json", "Act", "Atos dos Apóstolos"},
	"rm":               {"novotestamento/rm.json", "Rom", "Romanos"},
	"ro":               {"novotestamento/rm.json", "Rom", "Romanos"},
	"romanos":          {"novotestamento/rm.json", "Rom", "Romanos"},
	"1co":              {"novotestamento/1co.json", "1Cor", "1 Coríntios"},
	"1corintios":       {"novotestamento/1co.json", "1Cor", "1 Coríntios"},
	"2co":              {"novotestamento/2co.json", "2Cor", "2 Coríntios"},
	"2corintios":       {"novotestamento/2co.json", "2Cor", "2 Coríntios"},
	"gl":               {"novotestamento/gl.json", "Gal", "Gálatas"},
	"galatas":          {"novotestamento/gl.json", "Gal", "Gálatas"},
	"ef":               {"novotestamento/ef.json", "Eph", "Efésios"},
	"efesios":          {"novotestamento/ef.json", "Eph", "Efésios"},
	"fp":               {"novotestamento/fp.json", "Phil", "Filipenses"},
	"fl":               {"novotestamento/fp.json", "Phil", "Filipenses"},
	"filipenses":       {"novotestamento/fp.json", "Phil", "Filipenses"},
	"cl":               {"novotestamento/cl.json", "Col", "Colossenses"},
	"colossenses":      {"novotestamento/cl.json", "Col", "Colossenses"},
	"1ts":              {"novotestamento/1ts.json", "1Thess", "1 Tessalonicenses"},
	"1tessalonicenses": {"novotestamento/1ts.json", "1Thess", "1 Tessalonicenses"},
	"2ts":              {"novotestamento/2ts.json", "2Thess", "2 Tessalonicenses"},
	"2tessalonicenses": {"novotestamento/2ts.json", "2Thess", "2 Tessalonicenses"},
	"1tm":              {"novotestamento/1tm.json", "1Tim", "1 Timóteo"},
	"1timoteo":         {"novotestamento/1tm.json", "1Tim", "1 Timóteo"},
	"2tm":              {"novotestamento/2tm.json", "2Tim", "2 Timóteo"},
	"2timoteo":         {"novotestamento/2tm.json", "2Tim", "2 Timóteo"},
	"tt":               {"novotestamento/tt.json", "Tit", "Tito"},
	"tito":             {"novotestamento/tt.json", "Tit", "Tito"},
	"fm":               {"novotestamento/fm.json", "Phlm", "Filêmon"},
	"filemon":          {"novotestamento/fm.json", "Phlm", "Filêmon"},
	"hb":               {"novotestamento/hb.json", "Hebr", "Hebreus"},
	"hebreus":          {"novotestamento/hb.json", "Hebr", "Hebreus"},
	"tg":               {"novotestamento/tg.json", "Iac", "São Tiago"},
	"tiago":            {"novotestamento/tg.json", "Iac", "São Tiago"},
	"1pe":              {"novotestamento/1pe.json", "1Pet", "1 São Pedro"},
	"1pedro":           {"novotestamento/1pe.json", "1Pet", "1 São Pedro"},
	"2pe":              {"novotestamento/2pe.json", "2Pet", "2 São Pedro"},
	"2pedro":           {"novotestamento/2pe.json", "2Pet", "2 São Pedro"},
	"1jo":              {"novotestamento/1jo.json", "1Ioh", "1 São João"},
	"1joao":            {"novotestamento/1jo.json", "1Ioh", "1 São João"},
	"2jo":              {"novotestamento/2jo.json", "2Ioh", "2 São João"},
	"2joao":            {"novotestamento/2jo.json", "2Ioh", "2 São João"},
	"3jo":              {"novotestamento/3jo.json", "3Ioh", "3 São João"},
	"3joao":            {"novotestamento/3jo.json", "3Ioh", "3 São João"},
	"jda":              {"novotestamento/jda.json", "Iud", "São Judas"},
	"judas":            {"novotestamento/jda.json", "Iud", "São Judas"},
	"ap":               {"novotestamento/ap.json", "Apc", "Apocalipse"},
	"apocalipse":        {"novotestamento/ap.json", "Apc", "Apocalipse"},
}

// NormalizeBookKey cleans the book name string (lowercased, removes spaces and accents).
func NormalizeBookKey(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "á", "a")
	s = strings.ReplaceAll(s, "à", "a")
	s = strings.ReplaceAll(s, "ã", "a")
	s = strings.ReplaceAll(s, "â", "a")
	s = strings.ReplaceAll(s, "é", "e")
	s = strings.ReplaceAll(s, "ê", "e")
	s = strings.ReplaceAll(s, "í", "i")
	s = strings.ReplaceAll(s, "ó", "o")
	s = strings.ReplaceAll(s, "ô", "o")
	s = strings.ReplaceAll(s, "õ", "o")
	s = strings.ReplaceAll(s, "ú", "u")
	s = strings.ReplaceAll(s, "ç", "c")
	return s
}

// ParseReference extracts book, chapter, and verse range from text.
func ParseReference(input string) (def BookDefinition, chapter, startVerse, endVerse int, err error) {
	input = strings.TrimSpace(input)
	if input == "" || strings.EqualFold(input, "hoje") || strings.EqualFold(input, "aleatorio") || strings.EqualFold(input, "dia") {
		// Pick random / daily
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		chosen := dailyCatholicVerses[r.Intn(len(dailyCatholicVerses))]
		return ParseReference(chosen)
	}

	matches := reRef.FindStringSubmatch(input)
	if len(matches) < 4 {
		return BookDefinition{}, 0, 0, 0, fmt.Errorf("referência bíblica não reconhecida (ex: João 3:16 ou Sl 23:1)")
	}

	rawBook := matches[1]
	chapStr := matches[2]
	startStr := matches[3]
	endStr := matches[4]

	normKey := NormalizeBookKey(rawBook)
	bookDef, ok := bookRegistry[normKey]
	if !ok {
		// Try prefix match
		for k, v := range bookRegistry {
			if strings.HasPrefix(k, normKey) || strings.HasPrefix(normKey, k) {
				bookDef = v
				ok = true
				break
			}
		}
	}
	if !ok {
		return BookDefinition{}, 0, 0, 0, fmt.Errorf("livro bíblico não identificado: %s", rawBook)
	}

	chapter, _ = strconv.Atoi(chapStr)
	startVerse, _ = strconv.Atoi(startStr)
	endVerse = startVerse
	if endStr != "" {
		if ev, err := strconv.Atoi(endStr); err == nil && ev >= startVerse {
			endVerse = ev
		}
	}

	return bookDef, chapter, startVerse, endVerse, nil
}

// FetchPassage retrieves BOTH the Portuguese Catholic text and the Latin Vulgate for the requested passage.
func FetchPassage(ctx context.Context, reference string) (string, error) {
	bookDef, chapter, startVerse, endVerse, err := ParseReference(reference)
	if err != nil {
		return "", err
	}

	// 1. Fetch Portuguese Catholic text
	ptText, ptErr := fetchPortugueseCatholic(ctx, bookDef, chapter, startVerse, endVerse)

	// 2. Fetch Latin Vulgate text
	laText, laErr := fetchLatinVulgate(ctx, bookDef, chapter, startVerse, endVerse)

	if ptErr != nil && laErr != nil {
		return "", fmt.Errorf("não foi possível obter a passagem bíblica: %v", ptErr)
	}

	refLabel := fmt.Sprintf("%s %d:%d", bookDef.DisplayName, chapter, startVerse)
	if endVerse > startVerse {
		refLabel = fmt.Sprintf("%s %d:%d-%d", bookDef.DisplayName, chapter, startVerse, endVerse)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📖 *Bíblia Sagrada Católica — %s*\n\n", refLabel))

	if ptText != "" {
		sb.WriteString("🇧🇷 *Português (Tradução Pe. Manuel de Matos Soares):*\n")
		sb.WriteString(fmt.Sprintf("«%s»\n\n", ptText))
	} else if ptErr != nil {
		sb.WriteString(fmt.Sprintf("⚠️ _Texto em português indisponível no momento (%v)_\n\n", ptErr))
	}

	if laText != "" {
		sb.WriteString("🏛️ *Vulgata Latina (Nova Vulgata / Clementina):*\n")
		sb.WriteString(fmt.Sprintf("«%s»\n", laText))
	} else if laErr != nil {
		sb.WriteString(fmt.Sprintf("⚠️ _Texto em latim indisponível no momento (%v)_\n", laErr))
	}

	return sb.String(), nil
}

func fetchPortugueseCatholic(ctx context.Context, def BookDefinition, chapter, startVerse, endVerse int) (string, error) {
	var book PtBookData

	// Check cache
	if cached, ok := bookCache.Load(def.FilePath); ok {
		book = cached.(PtBookData)
	} else {
		// Download from Dancrf/biblia-db
		url := fmt.Sprintf("https://raw.githubusercontent.com/Dancrf/biblia-db/master/%s", def.FilePath)
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("erro de conexão: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("status github %d", resp.StatusCode)
		}

		if err := json.NewDecoder(resp.Body).Decode(&book); err != nil {
			return "", fmt.Errorf("decode json: %w", err)
		}

		bookCache.Store(def.FilePath, book)
	}

	// Find chapter
	var verses []string
	for _, c := range book.Capitulos {
		if c.Capitulo == chapter {
			for _, v := range c.Versiculos {
				if v.Numero >= startVerse && v.Numero <= endVerse {
					if startVerse != endVerse {
						verses = append(verses, fmt.Sprintf("%d. %s", v.Numero, strings.TrimSpace(v.Texto)))
					} else {
						verses = append(verses, strings.TrimSpace(v.Texto))
					}
				}
			}
			break
		}
	}

	if len(verses) == 0 {
		return "", fmt.Errorf("versículo %d:%d-%d não encontrado no livro %s", chapter, startVerse, endVerse, def.DisplayName)
	}

	return strings.Join(verses, " "), nil
}

func fetchLatinVulgate(ctx context.Context, def BookDefinition, chapter, startVerse, endVerse int) (string, error) {
	// Attempt 1: BibleGet I/O (Nova Vulgata NVBSE)
	cite := fmt.Sprintf("%s %d:%d", def.BibleGetCode, chapter, startVerse)
	if endVerse > startVerse {
		cite = fmt.Sprintf("%s %d:%d-%d", def.BibleGetCode, chapter, startVerse, endVerse)
	}

	client := &http.Client{Timeout: 8 * time.Second}
	apiURL := fmt.Sprintf("https://query.bibleget.io/v3/?query=%s&version=NVBSE", url.QueryEscape(cite))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err == nil {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			var bgResp BibleGetResponse
			if err := json.NewDecoder(resp.Body).Decode(&bgResp); err == nil && len(bgResp.Results) > 0 {
				var verses []string
				for _, r := range bgResp.Results {
					t := strings.TrimSpace(r.Text)
					if t != "" {
						if startVerse != endVerse {
							verses = append(verses, fmt.Sprintf("%s. %s", r.Verse, t))
						} else {
							verses = append(verses, t)
						}
					}
				}
				if len(verses) > 0 {
					return strings.Join(verses, " "), nil
				}
			}
		}
	}

	// Attempt 2: Fallback to bible-api.com Clementine Vulgate
	citeFallback := fmt.Sprintf("%s+%d:%d", def.BibleGetCode, chapter, startVerse)
	if def.BibleGetCode == "Jn" {
		citeFallback = fmt.Sprintf("john+%d:%d", chapter, startVerse)
	} else if def.BibleGetCode == "Mt" {
		citeFallback = fmt.Sprintf("matthew+%d:%d", chapter, startVerse)
	} else if def.BibleGetCode == "Mc" {
		citeFallback = fmt.Sprintf("mark+%d:%d", chapter, startVerse)
	} else if def.BibleGetCode == "Lc" {
		citeFallback = fmt.Sprintf("lucas+%d:%d", chapter, startVerse)
	} else if def.BibleGetCode == "Ps" {
		citeFallback = fmt.Sprintf("psalms+%d:%d", chapter, startVerse)
	} else if def.BibleGetCode == "Gn" {
		citeFallback = fmt.Sprintf("genesis+%d:%d", chapter, startVerse)
	}
	if endVerse > startVerse {
		citeFallback = fmt.Sprintf("%s-%d", citeFallback, endVerse)
	}

	fallbackURL := fmt.Sprintf("https://bible-api.com/%s?translation=clementine", citeFallback)
	req2, err2 := http.NewRequestWithContext(ctx, http.MethodGet, fallbackURL, nil)
	if err2 == nil {
		req2.Header.Set("User-Agent", "Mozilla/5.0")
		resp2, err2 := client.Do(req2)
		if err2 == nil && resp2.StatusCode == http.StatusOK {
			defer resp2.Body.Close()
			var bapi BibleAPIResponse
			if err := json.NewDecoder(resp2.Body).Decode(&bapi); err == nil && strings.TrimSpace(bapi.Text) != "" {
				return strings.TrimSpace(bapi.Text), nil
			}
		}
	}

	return "", fmt.Errorf("vulgata não encontrada para %s", cite)
}
