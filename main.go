package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

//go:embed index.html
var indexHTML []byte

//go:embed tclogo_default.png
var logoDefault []byte

var _ embed.FS

type Player struct {
	ID         int      `json:"id"`
	Nome       string   `json:"nome"`
	Cognome    string   `json:"cognome"`
	Classifica string   `json:"classifica"`
	Categoria  string   `json:"categoria"`
	Provincia  string   `json:"provincia"`
	Esito      string   `json:"esito"`
	Note       string   `json:"note"`
	Sesso      string   `json:"sesso"`
	Iscritto   bool     `json:"iscritto"`
	Forza      float64  `json:"forza"`
	Aggiornato string   `json:"aggiornato"`
	Riga       int      `json:"riga"`
	RankFile   string   `json:"rankFile"`
	Storico    []Cambio `json:"storico"`
}

type Cambio struct {
	Data  string `json:"data"`
	Da    string `json:"da"`
	A     string `json:"a"`
	Fonte string `json:"fonte"`
}

type Match struct {
	A     int    `json:"a"`
	B     int    `json:"b"`
	W     int    `json:"w"`
	Score string `json:"score"`
}

type Draw struct {
	Size    int         `json:"size"`
	Rounds  [][]Match   `json:"rounds"`
	Seeds   map[int]int `json:"seeds"`
	Created string      `json:"created"`
}

type State struct {
	Title   string           `json:"title"`
	Date    string           `json:"date"`
	Luogo   string           `json:"luogo"`
	NextID  int              `json:"nextId"`
	Source  string           `json:"source"`
	Players []Player         `json:"players"`
	Draws   map[string]*Draw `json:"draws"`

	FitpURL       string `json:"fitpUrl"`
	Aggiornamento string `json:"aggiornamento"`
	SourceMod     string `json:"sourceMod"`
	ServerMode    bool   `json:"serverMode"`
	TcURL         string `json:"tcUrl"`
}

var (
	serverMode bool
	st         State
	mu         sync.Mutex
	statePath  string
	dbPath     string
	dataDir    string
	lastPing   = time.Now()
	closingAt  time.Time
)

func nowStamp() string { return time.Now().Format("02/01/2006 15:04") }

// ---------- classifica / sesso ----------

var reNum = regexp.MustCompile(`^\d[.,]\d`)

func rankValue(s string) float64 {
	t := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if reNum.MatchString(t) {
		v, err := strconv.ParseFloat(strings.ReplaceAll(t[:3], ",", "."), 64)
		if err == nil {
			return v
		}
	}
	return 4.7 // 4.NC o dato mancante
}

var maleA = map[string]bool{"nicola": true, "andrea": true, "luca": true, "mattia": true, "elia": true, "tobia": true,
	"gianluca": true, "enea": true, "saba": true, "joshua": true, "jonathan": true, "ezra": true, "mirko": true}

func guessSex(first string) string {
	f := strings.Fields(strings.ToLower(strings.TrimSpace(first)))
	if len(f) == 0 {
		return "M"
	}
	n := f[0]
	if maleA[n] {
		return "M"
	}
	if strings.HasSuffix(n, "a") {
		return "F"
	}
	return "M"
}

func normSex(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch {
	case s == "F" || strings.HasPrefix(s, "FEM") || s == "DONNA":
		return "F"
	case s == "M" || strings.HasPrefix(s, "MAS") || s == "UOMO":
		return "M"
	}
	return ""
}

// ---------- import ----------

func cleanName(s string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(strings.TrimPrefix(s, "\ufeff")), "\""))
}

func importRows(rows [][]string) ([]Player, error) {
	if len(rows) < 2 {
		return nil, errors.New("il file e' vuoto")
	}
	idx := map[string]int{}
	for i, h := range rows[0] {
		idx[strings.ToLower(cleanName(h))] = i
	}
	find := func(names ...string) (int, bool) {
		for _, n := range names {
			if i, ok := idx[n]; ok {
				return i, true
			}
		}
		return 0, false
	}
	ni, ok1 := find("nome", "user_first_name", "first_name", "first name", "firstname")
	ci, ok2 := find("cognome", "user_last_name", "last_name", "last name", "lastname", "surname")
	if !ok1 || !ok2 {
		return nil, errors.New("nel file mancano le colonne del nome e del cognome (es. 'user_first_name' e 'user_last_name' dell'esportazione TennisCall, oppure 'Nome' e 'Cognome')")
	}
	get := func(r []string, name string) string {
		if i, ok := idx[name]; ok && i < len(r) {
			return strings.TrimSpace(r[i])
		}
		return ""
	}
	var out []Player
	id := 0
	for _, r := range rows[1:] {
		nome, cogn := "", ""
		if ni < len(r) {
			nome = cleanName(r[ni])
		}
		if ci < len(r) {
			cogn = cleanName(r[ci])
		}
		if nome == "" && cogn == "" {
			continue
		}
		if k := nameKey(nome); k == "segreteria" || k == "ospite" {
			continue // righe di servizio dell'esportazione TennisCall
		}
		id++
		p := Player{ID: id, Riga: id, Nome: nome, Cognome: cogn, Classifica: get(r, "classifica"), Categoria: get(r, "categoria"),
			Provincia: get(r, "provincia"), Esito: get(r, "esito"), Note: get(r, "note"), Iscritto: true}
		p.Sesso = normSex(get(r, "sesso"))
		if p.Sesso == "" {
			p.Sesso = guessSex(nome)
		}
		p.Forza = rankValue(p.Classifica)
		p.RankFile = p.Classifica
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errors.New("nessun giocatore trovato nel file")
	}
	return out, nil
}

type syncStats struct {
	Added, Renamed, Ranks int
	Notes                 []string
}

func (s syncStats) message(file string) string {
	if !s.changed() {
		return "Letto " + file + ": nessuna differenza rispetto all'elenco"
	}
	det := ""
	if len(s.Notes) > 0 {
		n := s.Notes
		if len(n) > 4 {
			n = append(n[:4:4], fmt.Sprintf("... altri %d", len(s.Notes)-4))
		}
		det = " (" + strings.Join(n, "; ") + ")"
	}
	return fmt.Sprintf("Letto %s: %d nuovi giocatori, %d nomi modificati%s, %d classifiche aggiornate", file, s.Added, s.Renamed, det, s.Ranks)
}

func (s syncStats) changed() bool { return s.Added+s.Renamed+s.Ranks > 0 }

func importData(data []byte, source string, force bool) (syncStats, error) {
	rows, err := readTable(data)
	if err != nil {
		return syncStats{}, err
	}
	pl, err := importRows(rows)
	if err != nil {
		return syncStats{}, err
	}
	return mergePlayers(pl, source, force), nil
}

// mergePlayers riallinea l'elenco dei giocatori al file:
//   - stesso nome e cognome: giocatore riconosciuto. La classifica del file viene
//     applicata se force (azione esplicita dell'utente) oppure se nel file e'
//     cambiata rispetto all'ultima lettura (cosi' un file vecchio non cancella
//     le classifiche aggiornate dal portale);
//   - nome o cognome modificati nel file: viene riconosciuto lo stesso giocatore
//     (stessa riga, oppure stesso nome/cognome) e aggiornato, mantenendo ID,
//     storico, iscrizione e tabelloni;
//   - righe nuove: nuovi giocatori.
func mergePlayers(pl []Player, source string, force bool) syncStats {
	var stats syncStats
	if st.NextID < 1 {
		st.NextID = 1
	}
	idx := map[string]int{}
	for i, p := range st.Players {
		idx[nameKey(p.Nome, p.Cognome)] = i
	}
	matched := map[int]bool{}
	seen := map[string]bool{}
	var rest []Player
	for _, n := range pl {
		k := nameKey(n.Nome, n.Cognome)
		if seen[k] {
			continue
		}
		seen[k] = true
		if i, ok := idx[k]; ok && !matched[i] {
			matched[i] = true
			p := &st.Players[i]
			p.Riga = n.Riga
			if n.Nome != p.Nome || n.Cognome != p.Cognome {
				// stesso giocatore, ma scritto diversamente (maiuscole, accenti, spazi, colonne scambiate)
				stats.Notes = append(stats.Notes, strings.TrimSpace(p.Cognome+" "+p.Nome)+" → "+strings.TrimSpace(n.Cognome+" "+n.Nome))
				p.Nome, p.Cognome = n.Nome, n.Cognome
				stats.Renamed++
			}
			apply := false
			if n.Classifica != "" && n.Classifica != p.Classifica {
				if force || (p.RankFile != "" && n.Classifica != p.RankFile) {
					apply = true
				}
			}
			if apply {
				setRank(p, n.Classifica, "file")
				stats.Ranks++
			}
			if apply || force {
				if n.Categoria != "" {
					p.Categoria = n.Categoria
				}
				if n.Provincia != "" {
					p.Provincia = n.Provincia
				}
				if n.Esito != "" {
					p.Esito = n.Esito
				}
				if n.Note != "" {
					p.Note = n.Note
				}
			}
			p.RankFile = n.Classifica
		} else {
			rest = append(rest, n)
		}
	}
	// giocatori che potrebbero essere stati rinominati nel file
	cand := func(i int) bool {
		p := st.Players[i]
		return !matched[i] && p.Esito != "AGGIUNTO A MANO"
	}
	rename := func(i int, n Player) {
		p := &st.Players[i]
		old := strings.TrimSpace(p.Cognome + " " + p.Nome)
		p.Nome, p.Cognome, p.Riga, p.RankFile = n.Nome, n.Cognome, n.Riga, n.Classifica
		p.Esito = "NOME MODIFICATO"
		p.Note = "Nome modificato nel file Excel (prima: " + old + "). Aggiorna dal portale per ricontrollarlo."
		matched[i] = true
		stats.Renamed++
		stats.Notes = append(stats.Notes, old+" → "+strings.TrimSpace(n.Cognome+" "+n.Nome))
		if n.Classifica != "" && n.Classifica != p.Classifica && force {
			setRank(p, n.Classifica, "file")
			stats.Ranks++
		}
	}
	var rest2 []Player
	// 1) stessa riga del file
	for _, n := range rest {
		done := false
		for i := range st.Players {
			if cand(i) && st.Players[i].Riga > 0 && st.Players[i].Riga == n.Riga {
				rename(i, n)
				done = true
				break
			}
		}
		if !done {
			rest2 = append(rest2, n)
		}
	}
	// 2) stesso cognome oppure stesso nome, se il candidato e' uno solo
	for _, n := range rest2 {
		var found []int
		for i := range st.Players {
			if !cand(i) {
				continue
			}
			p := st.Players[i]
			if (nameKey(p.Cognome) != "" && nameKey(p.Cognome) == nameKey(n.Cognome)) || (nameKey(p.Nome) != "" && nameKey(p.Nome) == nameKey(n.Nome)) {
				found = append(found, i)
			}
		}
		if len(found) == 1 {
			rename(found[0], n)
			continue
		}
		if len(found) == 0 {
			// 3) nome molto simile (correzione di refusi), se c'e' un solo candidato chiaro
			best, bestS, second := -1, 0.0, 0.0
			for i := range st.Players {
				if !cand(i) {
					continue
				}
				s := similarity(nameKey(st.Players[i].Nome, st.Players[i].Cognome), nameKey(n.Nome, n.Cognome))
				if s > bestS {
					best, second, bestS = i, bestS, s
				} else if s > second {
					second = s
				}
			}
			if best >= 0 && bestS >= 0.75 && bestS-second >= 0.1 {
				rename(best, n)
				continue
			}
		}
		n.ID = st.NextID
		st.NextID++
		if strings.TrimSpace(n.Classifica) == "" {
			n.Classifica, n.Forza = "4.NC", rankValue("4.NC")
			n.Esito, n.Note = "DA AGGIORNARE", "Classifica non presente nel file: usa Aggiorna dal portale FITP"
		}
		st.Players = append(st.Players, n)
		matched[len(st.Players)-1] = true
		stats.Added++
	}
	if st.Source != source {
		st.Source = source
	}
	return stats
}

// syncFromFile rilegge il file delle classifiche nella cartella del programma
// quando e' stato modificato (o sempre, con force). Restituisce anche un messaggio per l'utente.
func syncFromFile(force bool) (changed, found bool, msg string, err error) {
	f := findClassifiche()
	if f == "" {
		return false, false, "", nil
	}
	fi, err := os.Stat(f)
	if err != nil {
		return false, true, "", err
	}
	mod := strconv.FormatInt(fi.ModTime().UnixNano(), 10) + "|" + filepath.Base(f)
	if !force && mod == st.SourceMod {
		return false, true, "", nil
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return false, true, "", fmt.Errorf("non riesco a leggere %s (e' aperto in un altro programma?): %v", filepath.Base(f), err)
	}
	stats, err := importData(data, filepath.Base(f), force)
	if err != nil {
		return false, true, "", err
	}
	st.SourceMod = mod
	if stats.changed() {
		det := ""
		if len(stats.Notes) > 0 {
			n := stats.Notes
			if len(n) > 4 {
				n = append(n[:4:4], fmt.Sprintf("... altri %d", len(stats.Notes)-4))
			}
			det = " (" + strings.Join(n, "; ") + ")"
		}
		msg = fmt.Sprintf("Letto %s: %d nomi modificati%s, %d nuovi giocatori, %d classifiche aggiornate", filepath.Base(f), stats.Renamed, det, stats.Added, stats.Ranks)
	} else {
		msg = fmt.Sprintf("Letto %s (salvato il %s): nessuna differenza rispetto all'elenco", filepath.Base(f), fi.ModTime().Format("02/01/2006 15:04"))
	}
	return stats.changed(), true, msg, nil
}

func findLogo() string {
	dirs := []string{dataDir}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	for _, d := range dirs {
		es, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range es {
			if !e.IsDir() && strings.EqualFold(e.Name(), "tclogo.png") {
				return filepath.Join(d, e.Name())
			}
		}
	}
	return ""
}

// logoBytes restituisce tclogo.png dalla cartella del programma, se presente,
// altrimenti il logo incorporato nell'eseguibile.
func logoBytes() []byte {
	if p := findLogo(); p != "" {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return b
		}
	}
	return logoDefault
}

func findClassifiche() string {
	var dirs []string
	dirs = append(dirs, dataDir)
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	best, bestT := "", time.Time{}
	for _, d := range dirs {
		m, _ := filepath.Glob(filepath.Join(d, "*.xlsx"))
		for _, f := range m {
			base := filepath.Base(f)
			if !strings.Contains(strings.ToLower(base), "classifiche") || strings.HasPrefix(base, "~$") {
				continue
			}
			if fi, err := os.Stat(f); err == nil && (best == "" || fi.ModTime().After(bestT)) {
				best, bestT = f, fi.ModTime()
			}
		}
	}
	return best
}

// ---------- tabellone ----------

func seedOrder(size int) []int {
	arr := []int{1}
	for len(arr) < size {
		n := len(arr) * 2
		var next []int
		for _, s := range arr {
			next = append(next, s, n+1-s)
		}
		arr = next
	}
	return arr
}

func makeDraw(players []Player, seeds int, rng *rand.Rand) *Draw {
	n := len(players)
	size := 2
	for size < n {
		size *= 2
	}
	ps := append([]Player(nil), players...)
	rng.Shuffle(len(ps), func(i, j int) { ps[i], ps[j] = ps[j], ps[i] })
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Forza < ps[j].Forza })
	if seeds > n {
		seeds = n
	}
	if seeds > size/2 {
		seeds = size / 2
	}
	rest := append([]Player(nil), ps[seeds:]...)
	rng.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	var seq []int
	d := &Draw{Size: size, Seeds: map[int]int{}, Created: nowStamp()}
	for k := 0; k < seeds; k++ {
		seq = append(seq, ps[k].ID)
		d.Seeds[ps[k].ID] = k + 1
	}
	for _, p := range rest {
		seq = append(seq, p.ID)
	}
	order := seedOrder(size)
	slots := make([]int, size)
	for pos, s := range order {
		if s <= n {
			slots[pos] = seq[s-1]
		} else {
			slots[pos] = -1
		}
	}
	cnt := size / 2
	for cnt >= 1 {
		d.Rounds = append(d.Rounds, make([]Match, cnt))
		cnt /= 2
	}
	for i := range d.Rounds[0] {
		d.Rounds[0][i].A = slots[2*i]
		d.Rounds[0][i].B = slots[2*i+1]
	}
	recompute(d)
	return d
}

func recompute(d *Draw) {
	for r := range d.Rounds {
		for i := range d.Rounds[r] {
			m := &d.Rounds[r][i]
			if r > 0 {
				m.A = d.Rounds[r-1][2*i].W
				m.B = d.Rounds[r-1][2*i+1].W
			}
			switch {
			case m.A == -1 && m.B > 0:
				m.W, m.Score = m.B, ""
			case m.B == -1 && m.A > 0:
				m.W, m.Score = m.A, ""
			case m.A == -1 && m.B == -1:
				m.W = -1
			case m.W != 0 && m.W != m.A && m.W != m.B:
				m.W, m.Score = 0, ""
			}
		}
	}
}

// ---------- salvataggio ----------

type dbFile struct {
	Players       []Player `json:"players"`
	NextID        int      `json:"nextId"`
	Source        string   `json:"source"`
	FitpURL       string   `json:"fitpUrl"`
	Aggiornamento string   `json:"aggiornamento"`
	SourceMod     string   `json:"sourceMod"`
	TcURL         string   `json:"tcUrl"`
}

func writeJSONFile(path string, v interface{}) {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		os.Rename(tmp, path)
	}
}

// save scrive due file: il torneo (tabelloni, risultati) e il database interno
// delle classifiche (giocatori, classifiche, storico).
func save() {
	t := st
	t.Players, t.FitpURL, t.Aggiornamento, t.SourceMod, t.ServerMode, t.TcURL = nil, "", "", "", false, ""
	writeJSONFile(statePath, &t)
	writeJSONFile(dbPath, &dbFile{Players: st.Players, NextID: st.NextID, Source: st.Source, FitpURL: st.FitpURL, Aggiornamento: st.Aggiornamento, SourceMod: st.SourceMod, TcURL: st.TcURL})
}

func loadState() {
	st = State{Title: "Torneo Sociale TC Salemi", Luogo: "Tennis Club Salemi", NextID: 1, Draws: map[string]*Draw{}}
	if b, err := os.ReadFile(statePath); err == nil {
		var s State
		if json.Unmarshal(b, &s) == nil {
			st = s
			if st.Draws == nil {
				st.Draws = map[string]*Draw{}
			}
		}
	}
	var db dbFile
	if b, err := os.ReadFile(dbPath); err == nil && json.Unmarshal(b, &db) == nil {
		st.FitpURL, st.Aggiornamento, st.SourceMod, st.TcURL = db.FitpURL, db.Aggiornamento, db.SourceMod, db.TcURL
		if len(db.Players) > 0 {
			st.Players, st.NextID, st.Source = db.Players, db.NextID, db.Source
		}
	}
	// (se il database non esiste ancora, i giocatori della vecchia versione restano e vengono migrati da save())
	maxID := 0
	for _, p := range st.Players {
		if p.ID > maxID {
			maxID = p.ID
		}
	}
	if st.NextID <= maxID {
		st.NextID = maxID + 1
	}
	if st.NextID < 1 {
		st.NextID = 1
	}
	if st.Players == nil {
		st.Players = []Player{}
	}
	// giocatori della versione precedente: la riga di origine coincideva con l'ID
	for i := range st.Players {
		if st.Players[i].Riga == 0 && st.Players[i].Esito != "AGGIUNTO A MANO" {
			st.Players[i].Riga = st.Players[i].ID
		}
	}
	if _, _, _, err := syncFromFile(false); err != nil {
		log.Println("lettura file classifiche:", err)
	}
	save()
}

func pickDataDir(override string) string {
	if override != "" {
		if os.MkdirAll(override, 0755) == nil {
			return override
		}
	}
	var cands []string
	if ex, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Dir(ex))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, wd)
	}
	if h, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(h, "Documents"), h)
	}
	cands = append(cands, os.TempDir())
	for _, d := range cands {
		f := filepath.Join(d, ".scrittura_test")
		if os.WriteFile(f, []byte("x"), 0644) == nil {
			os.Remove(f)
			return d
		}
	}
	return "."
}

// ---------- apertura browser / file ----------

func openTarget(target string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		exec.Command("open", target).Start()
	default:
		exec.Command("xdg-open", target).Start()
	}
}

func openApp(url string) {
	if runtime.GOOS == "windows" {
		var cands []string
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
			base := os.Getenv(env)
			if base == "" {
				continue
			}
			cands = append(cands,
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
		}
		for _, c := range cands {
			if _, err := os.Stat(c); err == nil {
				if exec.Command(c, "--app="+url, "--window-size=1400,900").Start() == nil {
					return
				}
			}
		}
	}
	openTarget(url)
}

// ---------- API ----------

type apiFn func(r *http.Request) (interface{}, error)

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	if st.Players == nil {
		st.Players = []Player{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func api(fn apiFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		v, err := fn(r)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if r.Method == http.MethodPost {
			save()
		}
		writeJSON(w, 200, v)
	}
}

func readBody(r *http.Request, v interface{}) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func findPlayer(id int) *Player {
	for i := range st.Players {
		if st.Players[i].ID == id {
			return &st.Players[i]
		}
	}
	return nil
}

func savePDF(which string) (string, error) {
	b, err := buildPDF(which)
	if err != nil {
		return "", err
	}
	name := map[string]string{"all": "Tabelloni", "M": "Tabellone_Maschile", "F": "Tabellone_Femminile"}[which]
	path := filepath.Join(dataDir, fmt.Sprintf("TC_Salemi_%s_%s.pdf", name, time.Now().Format("2006-01-02_150405")))
	if err := os.WriteFile(path, b, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(indexHTML)
	})
	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(logoBytes())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/api/state", api(func(r *http.Request) (interface{}, error) { return &st, nil }))
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastPing = time.Now()
		closingAt = time.Time{}
		mu.Unlock()
		w.Write([]byte("tabellone-tc-salemi"))
	})
	mux.HandleFunc("/api/closing", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		closingAt = time.Now()
		mu.Unlock()
	})
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		if serverMode {
			http.Error(w, "non disponibile in modalita' server", http.StatusForbidden)
			return
		}
		mu.Lock()
		save()
		mu.Unlock()
		w.Write([]byte("ok"))
		go func() { time.Sleep(300 * time.Millisecond); os.Exit(0) }()
	})
	mux.HandleFunc("/api/settings", api(func(r *http.Request) (interface{}, error) {
		var b struct{ Title, Date, Luogo, FitpURL, TcURL string }
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		st.Title, st.Date, st.Luogo = strings.TrimSpace(b.Title), strings.TrimSpace(b.Date), strings.TrimSpace(b.Luogo)
		st.FitpURL = strings.TrimSpace(b.FitpURL)
		st.TcURL = strings.TrimSpace(b.TcURL)
		return &st, nil
	}))
	mux.HandleFunc("/api/player", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			ID         int     `json:"id"`
			Iscritto   *bool   `json:"iscritto"`
			Sesso      *string `json:"sesso"`
			Classifica *string `json:"classifica"`
			Nome       *string `json:"nome"`
			Cognome    *string `json:"cognome"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		p := findPlayer(b.ID)
		if p == nil {
			return nil, errors.New("giocatore non trovato")
		}
		if b.Iscritto != nil {
			p.Iscritto = *b.Iscritto
		}
		if b.Sesso != nil {
			if s := normSex(*b.Sesso); s != "" {
				p.Sesso = s
			}
		}
		if b.Classifica != nil {
			setRank(p, *b.Classifica, "manuale")
		}
		if b.Nome != nil {
			p.Nome = strings.TrimSpace(*b.Nome)
		}
		if b.Cognome != nil {
			p.Cognome = strings.TrimSpace(*b.Cognome)
		}
		return &st, nil
	}))
	mux.HandleFunc("/api/player/add", api(func(r *http.Request) (interface{}, error) {
		var b struct{ Nome, Cognome, Classifica, Sesso string }
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		if strings.TrimSpace(b.Nome) == "" && strings.TrimSpace(b.Cognome) == "" {
			return nil, errors.New("inserisci nome e cognome")
		}
		if st.NextID < 1 {
			st.NextID = len(st.Players) + 1
		}
		cl := strings.TrimSpace(b.Classifica)
		if cl == "" {
			cl = "4.NC"
		}
		p := Player{ID: st.NextID, Nome: strings.TrimSpace(b.Nome), Cognome: strings.TrimSpace(b.Cognome), Classifica: cl,
			Esito: "AGGIUNTO A MANO", Iscritto: true, Sesso: normSex(b.Sesso)}
		if p.Sesso == "" {
			p.Sesso = guessSex(p.Nome)
		}
		p.Forza = rankValue(p.Classifica)
		st.NextID++
		st.Players = append(st.Players, p)
		return &st, nil
	}))
	mux.HandleFunc("/api/player/delete", api(func(r *http.Request) (interface{}, error) {
		var b struct{ ID int }
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		for _, d := range st.Draws {
			for _, m := range d.Rounds[0] {
				if m.A == b.ID || m.B == b.ID {
					return nil, errors.New("il giocatore e' nel tabellone: elimina prima il tabellone, oppure toglilo dagli iscritti dopo un nuovo sorteggio")
				}
			}
		}
		for i := range st.Players {
			if st.Players[i].ID == b.ID {
				st.Players = append(st.Players[:i], st.Players[i+1:]...)
				break
			}
		}
		return &st, nil
	}))
	mux.HandleFunc("/api/import", api(func(r *http.Request) (interface{}, error) {
		if err := r.ParseMultipartForm(20 << 20); err != nil {
			return nil, err
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			return nil, errors.New("nessun file ricevuto")
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		stats, err := importData(data, h.Filename, true)
		if err != nil {
			return nil, err
		}
		return struct {
			*State
			Message string `json:"message"`
		}{&st, stats.message(h.Filename)}, nil
	}))
	mux.HandleFunc("/api/import/baseline", api(func(r *http.Request) (interface{}, error) {
		dlBase = csvSnapshot()
		return map[string]bool{"ok": true}, nil
	}))
	mux.HandleFunc("/api/import/downloads", api(func(r *http.Request) (interface{}, error) {
		if serverMode {
			return nil, errors.New("disponibile solo nel programma per PC: qui usa 'Carica file (CSV o Excel)...' per scegliere il file scaricato")
		}
		var b struct {
			OnlyNew bool `json:"onlyNew"`
		}
		readBody(r, &b)
		path, data, err := newestExport(b.OnlyNew)
		if err == errNoNewFile {
			return map[string]bool{"pending": true}, nil
		}
		if err != nil {
			return nil, err
		}
		stats, err := importData(data, filepath.Base(path), true)
		if err != nil {
			return nil, err
		}
		fi, _ := os.Stat(path)
		when := ""
		if fi != nil {
			when = " (scaricato il " + fi.ModTime().Format("02/01/2006 15:04") + ")"
		}
		return struct {
			*State
			Message string `json:"message"`
		}{&st, stats.message(filepath.Base(path)) + when}, nil
	}))
	mux.HandleFunc("/api/draw", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			Sex   string `json:"sex"`
			Seeds int    `json:"seeds"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		if b.Sex != "M" && b.Sex != "F" {
			return nil, errors.New("sesso non valido")
		}
		var pl []Player
		for _, p := range st.Players {
			if p.Iscritto && p.Sesso == b.Sex {
				pl = append(pl, p)
			}
		}
		if len(pl) < 2 {
			return nil, errors.New("servono almeno 2 iscritti per fare il tabellone")
		}
		if b.Seeds < 0 {
			b.Seeds = 0
		}
		st.Draws[b.Sex] = makeDraw(pl, b.Seeds, rand.New(rand.NewSource(time.Now().UnixNano())))
		return &st, nil
	}))
	mux.HandleFunc("/api/swap", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			Sex     string `json:"sex"`
			A       int    `json:"a"`
			B       int    `json:"b"`
			Confirm bool   `json:"confirm"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		d := st.Draws[b.Sex]
		if d == nil {
			return nil, errors.New("tabellone non trovato")
		}
		if b.A < 0 || b.B < 0 || b.A >= d.Size || b.B >= d.Size {
			return nil, errors.New("posizione non valida")
		}
		// partite coinvolte: quelle dei due posti e tutte quelle che li seguono fino alla finale
		type mp struct{ r, i int }
		var aff []mp
		seen := map[mp]bool{}
		for _, s := range []int{b.A, b.B} {
			for rr := range d.Rounds {
				k := mp{rr, (s / 2) >> uint(rr)}
				if !seen[k] {
					seen[k] = true
					aff = append(aff, k)
				}
			}
		}
		n := 0
		for _, k := range aff {
			m := d.Rounds[k.r][k.i]
			if m.A > 0 && m.B > 0 && m.W > 0 {
				n++
			}
		}
		if n > 0 && !b.Confirm {
			return nil, fmt.Errorf("lo scambio azzera %d risultati gia' inseriti: conferma l'operazione", n)
		}
		ptr := func(k int) *int {
			if k%2 == 0 {
				return &d.Rounds[0][k/2].A
			}
			return &d.Rounds[0][k/2].B
		}
		pa, pb := ptr(b.A), ptr(b.B)
		*pa, *pb = *pb, *pa
		for _, k := range aff {
			m := &d.Rounds[k.r][k.i]
			m.W, m.Score = 0, ""
		}
		recompute(d)
		return &st, nil
	}))
	mux.HandleFunc("/api/enter", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var b struct {
			Sex     string `json:"sex"`
			ID      int    `json:"id"`
			Round   int    `json:"round"` // turno di ingresso, da 1 (primo turno)
			Confirm bool   `json:"confirm"`
		}
		if err := readBody(r, &b); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		msg, need, err := enterRound(b.Sex, b.ID, b.Round, b.Confirm)
		if need > 0 {
			writeJSON(w, 409, map[string]interface{}{"error": fmt.Sprintf("l'operazione azzera %d risultati gia' inseriti", need), "needConfirm": true, "count": need})
			return
		}
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		save()
		writeJSON(w, 200, struct {
			*State
			Message string `json:"message"`
		}{&st, msg})
	})
	mux.HandleFunc("/api/seed", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			Sex  string `json:"sex"`
			ID   int    `json:"id"`
			Seed int    `json:"seed"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		d := st.Draws[b.Sex]
		if d == nil {
			return nil, errors.New("tabellone non trovato")
		}
		found := false
		for _, m := range d.Rounds[0] {
			if m.A == b.ID || m.B == b.ID {
				found = true
			}
		}
		if !found || b.ID <= 0 {
			return nil, errors.New("il giocatore non e' nel tabellone")
		}
		if d.Seeds == nil {
			d.Seeds = map[int]int{}
		}
		if b.Seed <= 0 {
			delete(d.Seeds, b.ID)
			return &st, nil
		}
		if b.Seed > d.Size {
			return nil, fmt.Errorf("il numero deve essere tra 1 e %d", d.Size)
		}
		for id, s := range d.Seeds {
			if s == b.Seed && id != b.ID {
				name := ""
				if p := findPlayer(id); p != nil {
					name = strings.TrimSpace(p.Cognome + " " + p.Nome)
				}
				return nil, fmt.Errorf("il numero %d e' gia' assegnato a %s: cambia prima quello", b.Seed, name)
			}
		}
		d.Seeds[b.ID] = b.Seed
		return &st, nil
	}))
	mux.HandleFunc("/api/draw/delete", api(func(r *http.Request) (interface{}, error) {
		var b struct{ Sex string }
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		delete(st.Draws, b.Sex)
		return &st, nil
	}))
	mux.HandleFunc("/api/result", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			Sex    string  `json:"sex"`
			Round  int     `json:"round"`
			Match  int     `json:"match"`
			Winner *int    `json:"winner"`
			Score  *string `json:"score"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		d := st.Draws[b.Sex]
		if d == nil || b.Round < 0 || b.Round >= len(d.Rounds) || b.Match < 0 || b.Match >= len(d.Rounds[b.Round]) {
			return nil, errors.New("partita non trovata")
		}
		m := &d.Rounds[b.Round][b.Match]
		if m.A <= 0 || m.B <= 0 {
			return nil, errors.New("la partita non e' ancora definita")
		}
		if b.Winner != nil {
			if *b.Winner != 0 && *b.Winner != m.A && *b.Winner != m.B {
				return nil, errors.New("vincitore non valido")
			}
			m.W = *b.Winner
		}
		if b.Score != nil {
			s := strings.TrimSpace(*b.Score)
			if len([]rune(s)) > 40 {
				s = string([]rune(s)[:40])
			}
			m.Score = s
		}
		recompute(d)
		return &st, nil
	}))
	mux.HandleFunc("/api/fitp/start", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			IDs          []int `json:"ids"`
			OnlyIscritti bool  `json:"onlyIscritti"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		if err := startUpdate(b.IDs, b.OnlyIscritti); err != nil {
			return nil, err
		}
		return fjob, nil
	}))
	mux.HandleFunc("/api/fitp/cancel", api(func(r *http.Request) (interface{}, error) {
		if fjob.Running {
			fjob.Cancel = true
		}
		return fjob, nil
	}))
	mux.HandleFunc("/api/fitp/test", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		url := strings.TrimSpace(st.FitpURL)
		mu.Unlock()
		if url == "" {
			url = defaultFitpURL
		}
		writeJSON(w, 200, fitpProbe(url))
	})
	mux.HandleFunc("/api/fitp/status", api(func(r *http.Request) (interface{}, error) { return fjob, nil }))
	mux.HandleFunc("/api/pdf/save", api(func(r *http.Request) (interface{}, error) {
		var b struct{ Which string }
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		if b.Which != "M" && b.Which != "F" {
			b.Which = "all"
		}
		path, err := savePDF(b.Which)
		if err != nil {
			return nil, err
		}
		openTarget(path)
		return map[string]string{"path": path}, nil
	}))
	mux.HandleFunc("/api/pdf", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		which := r.URL.Query().Get("which")
		if which != "M" && which != "F" {
			which = "all"
		}
		b, err := buildPDF(which)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		fn := map[string]string{"all": "Tabelloni", "M": "Tabellone_Maschile", "F": "Tabellone_Femminile"}[which]
		w.Header().Set("Content-Disposition", `attachment; filename="TC_Salemi_`+fn+`.pdf"`)
		w.Write(b)
	})
	return mux
}

func isOurs(port int) bool {
	cl := &http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 100))
	return bytes.Equal(bytes.TrimSpace(b), []byte("tabellone-tc-salemi"))
}

func basicAuth(next http.Handler, user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			time.Sleep(700 * time.Millisecond)
			w.Header().Set("WWW-Authenticate", `Basic realm="Torneo TC Salemi", charset="UTF-8"`)
			http.Error(w, "Accesso riservato", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func envOr(flagVal, env string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(env)
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func main() {
	port := flag.Int("port", 8765, "porta locale (modalita' desktop)")
	noBrowser := flag.Bool("nobrowser", false, "non aprire il browser")
	addrF := flag.String("addr", "", "modalita' server: indirizzo di ascolto, es. 0.0.0.0:8080 (oppure variabile TORNEO_ADDR)")
	dataF := flag.String("data", "", "cartella dei dati e del file classifiche (oppure TORNEO_DATA)")
	userF := flag.String("user", "", "modalita' server: nome utente (predefinito tcsalemi, oppure TORNEO_USER)")
	passF := flag.String("password", "", "modalita' server: password di accesso (oppure TORNEO_PASSWORD)")
	flag.Parse()

	addr := envOr(*addrF, "TORNEO_ADDR")
	if addr == "" {
		if p := os.Getenv("PORT"); p != "" { // Render, Railway, Heroku ecc.
			addr = "0.0.0.0:" + p
		}
	}
	serverMode = addr != ""
	if serverMode && os.Getenv("TZ") == "" {
		if loc, err := time.LoadLocation("Europe/Rome"); err == nil {
			time.Local = loc
		}
	}
	dataDir = pickDataDir(envOr(*dataF, "TORNEO_DATA"))
	statePath = filepath.Join(dataDir, "torneo_tc_salemi.json")
	dbPath = filepath.Join(dataDir, "classifica_db.json")
	if !serverMode {
		if lf, err := os.OpenFile(filepath.Join(dataDir, "torneo_log.txt"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
			log.SetOutput(lf)
		}
	}
	loadState()
	st.ServerMode = serverMode

	if serverMode {
		user := envOr(*userF, "TORNEO_USER")
		if user == "" {
			user = "tcsalemi"
		}
		pass := envOr(*passF, "TORNEO_PASSWORD")
		if pass == "" && !isLoopback(addr) {
			fmt.Fprintln(os.Stderr, "ERRORE: in modalita' server serve una password (opzione -password o variabile TORNEO_PASSWORD).")
			os.Exit(1)
		}
		var h http.Handler = routes()
		if pass != "" {
			h = basicAuth(h, user, pass)
		}
		srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute}
		log.Printf("Tabellone TC Salemi (modalita' server) in ascolto su %s, dati in %s", addr, dataDir)
		fmt.Printf("Tabellone TC Salemi (modalita' server) in ascolto su %s - dati in %s\n", addr, dataDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
		return
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		if isOurs(*port) {
			if !*noBrowser {
				openApp(fmt.Sprintf("http://127.0.0.1:%d/", *port))
			}
			return
		}
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Fatal(err)
		}
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	fmt.Println("Tabellone TC Salemi in ascolto su", url)
	srv := &http.Server{Handler: routes()}
	go func() {
		for {
			time.Sleep(2 * time.Second)
			mu.Lock()
			idle := time.Since(lastPing) > 3*time.Minute
			closed := !closingAt.IsZero() && time.Since(closingAt) > 12*time.Second
			if idle || closed {
				save()
				mu.Unlock()
				srv.Shutdown(context.Background())
				os.Exit(0)
			}
			mu.Unlock()
		}
	}()
	if !*noBrowser {
		openApp(url)
	}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

var accents = strings.NewReplacer("à", "a", "á", "a", "â", "a", "ä", "a", "è", "e", "é", "e", "ê", "e", "ë", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ò", "o", "ó", "o", "ô", "o", "ö", "o", "ù", "u", "ú", "u", "û", "u", "ü", "u")

// similarity restituisce un valore tra 0 e 1 (1 = stringhe uguali).
func similarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			m := prev[j-1] + c
			if prev[j]+1 < m {
				m = prev[j] + 1
			}
			if cur[j-1]+1 < m {
				m = cur[j-1] + 1
			}
			cur[j] = m
		}
		prev = cur
	}
	max := len(ra)
	if len(rb) > max {
		max = len(rb)
	}
	return 1 - float64(prev[len(rb)])/float64(max)
}

func playerName(id int) string {
	if p := findPlayer(id); p != nil {
		return strings.TrimSpace(p.Cognome + " " + p.Nome)
	}
	return "?"
}

// enterRound fa entrare il giocatore id al turno R (1 = primo turno): lo "spicchio" di
// tabellone che precede quel turno viene liberato con dei BYE, spostando altrove i
// giocatori che c'erano (verso posti occupati da BYE). Va chiamata con `mu` acquisito.
// Se servono risultati da azzerare e confirm e' falso restituisce need > 0 senza modificare nulla.
func enterRound(sex string, id, R int, confirm bool) (msg string, need int, err error) {
	d := st.Draws[sex]
	if d == nil {
		return "", 0, errors.New("tabellone non trovato")
	}
	rounds := len(d.Rounds)
	if R < 1 || R > rounds {
		return "", 0, fmt.Errorf("il turno deve essere tra 1 e %d", rounds)
	}
	slots := make([]int, d.Size)
	for i, m := range d.Rounds[0] {
		slots[2*i], slots[2*i+1] = m.A, m.B
	}
	pos := -1
	for k, v := range slots {
		if v == id && id > 0 {
			pos = k
		}
	}
	if pos < 0 {
		return "", 0, errors.New("il giocatore non e' nel tabellone")
	}
	bs := 1 << uint(R-1)
	start := pos / bs * bs
	sib := start ^ bs // spicchio avversario
	inBlock := func(k, s int) bool { return k >= s && k < s+bs }
	changed := map[int]bool{}
	swap := func(a, b int) {
		slots[a], slots[b] = slots[b], slots[a]
		changed[a], changed[b] = true, true
	}
	var moved []string

	// 1) libera lo spicchio del giocatore: tutti gli altri posti diventano BYE
	var intr []int
	for k := start; k < start+bs; k++ {
		if k != pos && slots[k] > 0 {
			intr = append(intr, k)
		}
	}
	type cb struct{ slot, cost int }
	var byes []cb
	for k := 0; k < d.Size; k++ {
		if inBlock(k, start) || slots[k] != -1 {
			continue
		}
		cost := 0
		if p := slots[k^1]; p == -1 {
			cost = 1
		} else if p > 0 && d.Seeds[p] > 0 {
			cost = 2 // non togliere il BYE a un'altra testa di serie
		}
		byes = append(byes, cb{k, cost})
	}
	if len(intr) > len(byes) {
		return "", 0, fmt.Errorf("non ci sono abbastanza BYE: per far entrare %s al turno %d servono %d BYE fuori dal suo gruppo, ne sono disponibili %d", playerName(id), R, len(intr), len(byes))
	}
	sort.SliceStable(byes, func(i, j int) bool {
		if byes[i].cost != byes[j].cost {
			return byes[i].cost < byes[j].cost
		}
		return byes[i].slot < byes[j].slot
	})
	for i, k := range intr {
		swap(k, byes[i].slot)
		moved = append(moved, playerName(slots[byes[i].slot]))
	}

	// 2) lo spicchio avversario deve contenere almeno un giocatore, altrimenti
	//    il giocatore entrerebbe piu' avanti: porto li' un giocatore da fuori
	hasReal := false
	for k := sib; k < sib+bs; k++ {
		if slots[k] > 0 {
			hasReal = true
		}
	}
	if !hasReal {
		pick := func(strict bool) int {
			best, bestF := -1, -1.0
			for k, v := range slots {
				if v <= 0 || inBlock(k, start) || inBlock(k, sib) {
					continue
				}
				if strict && (d.Seeds[v] > 0 || slots[k^1] <= 0) {
					continue
				}
				f := 0.0
				if p := findPlayer(v); p != nil {
					f = p.Forza
				}
				if f > bestF {
					best, bestF = k, f
				}
			}
			return best
		}
		x := pick(true)
		if x < 0 {
			x = pick(false)
		}
		if x < 0 {
			return "", 0, errors.New("non ci sono giocatori da portare nello spicchio avversario")
		}
		swap(x, sib)
		moved = append(moved, playerName(slots[sib])+" (nello spicchio avversario)")
	}

	if len(changed) == 0 {
		return playerName(id) + " entra gia' al turno " + strconv.Itoa(R) + ": nessuna modifica", 0, nil
	}

	// 3) partite coinvolte: i posti cambiati e i turni che li seguono
	type mp struct{ r, i int }
	var aff []mp
	seen := map[mp]bool{}
	for s := range changed {
		for rr := range d.Rounds {
			k := mp{rr, (s / 2) >> uint(rr)}
			if !seen[k] {
				seen[k] = true
				aff = append(aff, k)
			}
		}
	}
	n := 0
	for _, k := range aff {
		m := d.Rounds[k.r][k.i]
		if m.A > 0 && m.B > 0 && m.W > 0 {
			n++
		}
	}
	if n > 0 && !confirm {
		return "", n, nil
	}
	for i := range d.Rounds[0] {
		d.Rounds[0][i].A, d.Rounds[0][i].B = slots[2*i], slots[2*i+1]
	}
	for _, k := range aff {
		m := &d.Rounds[k.r][k.i]
		m.W, m.Score = 0, ""
	}
	recompute(d)
	msg = fmt.Sprintf("%s ora entra al turno %d.", playerName(id), R)
	if len(moved) > 0 {
		msg += " Spostati: " + strings.Join(moved, ", ") + "."
	}
	return msg, 0, nil
}

const defaultTcURL = "https://admin.tenniscall.com/it/tennis-center/export-customers?id=6435"

var dlBase map[string]string // CSV presenti prima di avviare il download

func downloadDirs() []string {
	var dirs []string
	if h, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, "Downloads"), filepath.Join(h, "Scaricati"))
	}
	return append(dirs, dataDir)
}

func csvSnapshot() map[string]string {
	m := map[string]string{}
	for _, d := range downloadDirs() {
		fs, _ := filepath.Glob(filepath.Join(d, "*.csv"))
		for _, f := range fs {
			if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
				m[f] = fmt.Sprintf("%d|%d", fi.ModTime().UnixNano(), fi.Size())
			}
		}
	}
	return m
}

var errNoNewFile = errors.New("nessun nuovo file")

// newestExport cerca il CSV piu' recente che contenga davvero un elenco di giocatori
// (colonne nome/cognome). Con onlyNew considera solo i file comparsi o cambiati
// rispetto allo snapshot preso prima dell'avvio del download.
func newestExport(onlyNew bool) (string, []byte, error) {
	type cf struct {
		path string
		mod  time.Time
	}
	var cands []cf
	for f, sig := range csvSnapshot() {
		if onlyNew && dlBase != nil && dlBase[f] == sig {
			continue
		}
		if fi, err := os.Stat(f); err == nil {
			cands = append(cands, cf{f, fi.ModTime()})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	for _, c := range cands {
		data, err := os.ReadFile(c.path)
		if err != nil {
			continue
		}
		rows, err := readTable(data)
		if err != nil {
			continue
		}
		if _, err := importRows(rows); err == nil {
			return c.path, data, nil
		}
	}
	if onlyNew {
		return "", nil, errNoNewFile
	}
	return "", nil, errors.New("non ho trovato nessun CSV con l'elenco giocatori nella cartella Download. Su TennisCall premi 'Esporta (.csv)', poi riprova (oppure usa 'Carica file (CSV o Excel)...')")
}
