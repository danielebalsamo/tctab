package main

// extra.go: punteggio per set, gironi all'italiana, doppi, classifica di circolo
// (con la classifica interna dei 4.NC), backup/ripristino e dati per la pagina pubblica.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- categorie ----------

var catKeys = []string{"M", "F", "DM", "DF", "DX"}

func validCat(k string) bool {
	for _, c := range catKeys {
		if c == k {
			return true
		}
	}
	return false
}

func isDoubles(k string) bool { return strings.HasPrefix(k, "D") }

func catTitle(k string) string {
	switch k {
	case "M":
		return "Singolare maschile"
	case "F":
		return "Singolare femminile"
	case "DM":
		return "Doppio maschile"
	case "DF":
		return "Doppio femminile"
	case "DX":
		return "Doppio misto"
	}
	return k
}

func pdfName(which string) string {
	switch which {
	case "M":
		return "Tabellone_Maschile"
	case "F":
		return "Tabellone_Femminile"
	case "DM":
		return "Doppio_Maschile"
	case "DF":
		return "Doppio_Femminile"
	case "DX":
		return "Doppio_Misto"
	}
	return "Tabelloni"
}

func todayISO() string { return time.Now().Format("2006-01-02") }

func normFmt(s string) string {
	if s == "stb" {
		return "stb"
	}
	return "bo3"
}

func findAny(id int) *Player {
	if p := findPlayer(id); p != nil {
		return p
	}
	for i := range st.Coppie {
		if st.Coppie[i].ID == id {
			return &st.Coppie[i]
		}
	}
	return nil
}

func playersForKey(key string) ([]Player, error) {
	if !validCat(key) {
		return nil, errors.New("categoria non valida")
	}
	var pl []Player
	if isDoubles(key) {
		for _, c := range st.Coppie {
			if c.Cat == key {
				pl = append(pl, c)
			}
		}
		if len(pl) < 2 {
			return nil, errors.New("servono almeno 2 coppie")
		}
		return pl, nil
	}
	for _, p := range st.Players {
		if p.Iscritto && p.Sesso == key {
			pl = append(pl, p)
		}
	}
	if len(pl) < 2 {
		return nil, errors.New("servono almeno 2 iscritti per fare il tabellone")
	}
	return pl, nil
}

// ---------- set e punteggi ----------

type SetRes struct {
	A int `json:"a"`
	B int `json:"b"`
}

func (m *Match) reset() {
	m.W, m.Score, m.Sets, m.Ex, m.Data = 0, "", nil, "", ""
}

var reSetTok = regexp.MustCompile(`^(\d+)\s*[-/:]\s*(\d+)`)

// parseScore legge un punteggio scritto a mano ("6-4 3-6 [10-7]") e lo trasforma in set.
func parseScore(s string) []SetRes {
	var out []SetRes
	clean := strings.NewReplacer("[", " ", "]", " ").Replace(s)
	for _, f := range strings.Fields(clean) {
		m := reSetTok.FindStringSubmatch(f)
		if m == nil {
			continue
		}
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		out = append(out, SetRes{A: a, B: b})
	}
	return out
}

func matchSets(m *Match) []SetRes {
	if len(m.Sets) > 0 {
		return m.Sets
	}
	return parseScore(m.Score)
}

// decideSets controlla i set inseriti e dice chi ha vinto (1 = A, 2 = B).
// perde e' il lato (1/2) che non si presenta o si ritira.
func decideSets(sets []SetRes, format, ex string, perde int) (int, error) {
	if len(sets) > 3 {
		return 0, errors.New("al massimo 3 set")
	}
	if ex == "wo" {
		return 3 - perde, nil
	}
	wa, wb := 0, 0
	for i, s := range sets {
		if s.A < 0 || s.B < 0 {
			return 0, errors.New("punteggio non valido")
		}
		if wa == 2 || wb == 2 {
			return 0, errors.New("il match era gia' finito: c'e' un set in piu'")
		}
		last := i == len(sets)-1
		if !(ex == "rit" && last) {
			hi, lo := s.A, s.B
			if lo > hi {
				hi, lo = lo, hi
			}
			if format == "stb" && i == 2 {
				if hi < 10 || hi-lo < 2 {
					return 0, errors.New("il tie-break a 10 si vince con almeno 10 punti e 2 di scarto (es. 10-7)")
				}
			} else if !((hi >= 6 && hi-lo >= 2) || (hi == 7 && lo == 6)) {
				return 0, fmt.Errorf("set %d non valido (es. 6-4, 7-5, 7-6)", i+1)
			}
		}
		if s.A > s.B {
			wa++
		} else if s.B > s.A {
			wb++
		} else if !(ex == "rit" && last) {
			return 0, fmt.Errorf("set %d in parita'", i+1)
		}
	}
	if ex == "rit" {
		return 3 - perde, nil
	}
	if wa == 2 {
		return 1, nil
	}
	if wb == 2 {
		return 2, nil
	}
	return 0, errors.New("punteggio incompleto: serve chi vince 2 set")
}

func scoreString(sets []SetRes, format, ex string) string {
	if ex == "wo" {
		return "W.O."
	}
	var parts []string
	for i, s := range sets {
		if format == "stb" && i == 2 {
			parts = append(parts, fmt.Sprintf("[%d-%d]", s.A, s.B))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", s.A, s.B))
		}
	}
	out := strings.Join(parts, " ")
	if ex == "rit" {
		out = strings.TrimSpace(out + " rit.")
	}
	return out
}

func applyResult(m *Match, format string, sets []SetRes, ex, perde string) error {
	if m.A <= 0 || m.B <= 0 {
		return errors.New("la partita non e' ancora definita")
	}
	if ex != "" && ex != "wo" && ex != "rit" {
		return errors.New("esito non valido")
	}
	side := 0
	if perde == "A" {
		side = 1
	} else if perde == "B" {
		side = 2
	}
	if ex != "" && side == 0 {
		return errors.New("indica chi si ritira o non si presenta")
	}
	w, err := decideSets(sets, format, ex, side)
	if err != nil {
		return err
	}
	if ex == "wo" {
		sets = nil
	}
	m.Sets, m.Ex = sets, ex
	if w == 1 {
		m.W = m.A
	} else {
		m.W = m.B
	}
	m.Score = scoreString(sets, format, ex)
	m.Data = todayISO()
	return nil
}

// gamesOf conta i game di A e B; il tie-break a 10 vale 1 game al vincitore.
func gamesOf(sets []SetRes, format string) (int, int) {
	ga, gb := 0, 0
	for i, s := range sets {
		if format == "stb" && i == 2 {
			if s.A > s.B {
				ga++
			} else if s.B > s.A {
				gb++
			}
			continue
		}
		ga += s.A
		gb += s.B
	}
	return ga, gb
}

func setsWon(sets []SetRes) (int, int) {
	a, b := 0, 0
	for _, s := range sets {
		if s.A > s.B {
			a++
		} else if s.B > s.A {
			b++
		}
	}
	return a, b
}

// ---------- gironi all'italiana ----------

type RRMatch struct {
	Match
	G int `json:"g"`
	R int `json:"r"`
}

type RR struct {
	Fmt     string    `json:"fmt"`
	Groups  [][]int   `json:"groups"`
	Matches []RRMatch `json:"matches"`
	Created string    `json:"created"`
}

type Stand struct {
	ID  int `json:"id"`
	G   int `json:"g"`
	V   int `json:"v"`
	P   int `json:"p"`
	SV  int `json:"sv"`
	SP  int `json:"sp"`
	GV  int `json:"gv"`
	GP  int `json:"gp"`
	Pt  int `json:"pt"`
	Pos int `json:"pos"`
}

// roundRobinPairs restituisce le coppie {A, B, turno} con il metodo del cerchio.
func roundRobinPairs(ids []int) [][3]int {
	arr := append([]int(nil), ids...)
	if len(arr)%2 == 1 {
		arr = append(arr, 0)
	}
	n := len(arr)
	var out [][3]int
	for r := 0; r < n-1; r++ {
		for i := 0; i < n/2; i++ {
			a, b := arr[i], arr[n-1-i]
			if a != 0 && b != 0 {
				out = append(out, [3]int{a, b, r + 1})
			}
		}
		last := arr[n-1]
		copy(arr[2:], arr[1:n-1])
		arr[1] = last
	}
	return out
}

func makeRR(players []Player, ngroups int, rng *rand.Rand) *RR {
	n := len(players)
	if ngroups < 1 {
		ngroups = 1
	}
	if ngroups > n/2 {
		ngroups = n / 2
	}
	if ngroups < 1 {
		ngroups = 1
	}
	ps := append([]Player(nil), players...)
	rng.Shuffle(len(ps), func(i, j int) { ps[i], ps[j] = ps[j], ps[i] })
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Forza != ps[j].Forza {
			return ps[i].Forza < ps[j].Forza
		}
		return internalPts[ps[i].ID] > internalPts[ps[j].ID]
	})
	groups := make([][]int, ngroups)
	for i, p := range ps { // distribuzione "a serpentina": le teste di serie finiscono in gironi diversi
		row, col := i/ngroups, i%ngroups
		if row%2 == 1 {
			col = ngroups - 1 - col
		}
		groups[col] = append(groups[col], p.ID)
	}
	rr := &RR{Groups: groups, Created: nowStamp()}
	for gi, g := range groups {
		for _, pr := range roundRobinPairs(g) {
			rr.Matches = append(rr.Matches, RRMatch{Match: Match{A: pr[0], B: pr[1]}, G: gi, R: pr[2]})
		}
	}
	return rr
}

// rrStandings: 2 punti per vittoria, 1 per sconfitta giocata, 0 per W.O. subito.
// A pari punti: scontro diretto (solo se sono in due), poi differenza set e differenza game.
func rrStandings(rr *RR) [][]Stand {
	res := make([][]Stand, len(rr.Groups))
	for gi, g := range rr.Groups {
		m := map[int]*Stand{}
		for _, id := range g {
			m[id] = &Stand{ID: id}
		}
		h2h := map[[2]int]bool{}
		for i := range rr.Matches {
			x := &rr.Matches[i]
			if x.G != gi || x.W <= 0 {
				continue
			}
			a, b := m[x.A], m[x.B]
			if a == nil || b == nil {
				continue
			}
			win, los := a, b
			if x.W == x.B {
				win, los = b, a
			}
			h2h[[2]int{win.ID, los.ID}] = true
			a.G++
			b.G++
			win.V++
			los.P++
			win.Pt += 2
			if x.Ex != "wo" {
				los.Pt++
				sets := matchSets(&x.Match)
				sa, sb := setsWon(sets)
				ga, gb := gamesOf(sets, rr.Fmt)
				a.SV, a.SP, a.GV, a.GP = a.SV+sa, a.SP+sb, a.GV+ga, a.GP+gb
				b.SV, b.SP, b.GV, b.GP = b.SV+sb, b.SP+sa, b.GV+gb, b.GP+ga
			}
		}
		list := make([]Stand, 0, len(g))
		cnt := map[int]int{}
		for _, id := range g {
			list = append(list, *m[id])
			cnt[m[id].Pt]++
		}
		sort.SliceStable(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if a.Pt != b.Pt {
				return a.Pt > b.Pt
			}
			if cnt[a.Pt] == 2 {
				ab, ba := h2h[[2]int{a.ID, b.ID}], h2h[[2]int{b.ID, a.ID}]
				if ab != ba {
					return ab
				}
			}
			if da, db := a.SV-a.SP, b.SV-b.SP; da != db {
				return da > db
			}
			if da, db := a.GV-a.GP, b.GV-b.GP; da != db {
				return da > db
			}
			return a.ID < b.ID
		})
		for i := range list {
			list[i].Pos = i + 1
		}
		res[gi] = list
	}
	return res
}

// ---------- classifica di circolo ----------

// PuntiParams: parametri del calcolo punti (modificabili dall'interfaccia).
type PuntiParams struct {
	Vittoria  float64 `json:"vittoria"`
	Sconfitta float64 `json:"sconfitta"`
	Mesi      int     `json:"mesi"`
}

func defaultParams() PuntiParams { return PuntiParams{Vittoria: 10, Sconfitta: 6, Mesi: 12} }

type LedgerRow struct {
	Key    string         `json:"key"`
	Torneo int            `json:"torneo"`
	Titolo string         `json:"titolo"`
	Data   string         `json:"data"`
	Cat    string         `json:"cat"`
	Turno  string         `json:"turno"`
	P1     []int          `json:"p1"`
	P2     []int          `json:"p2"`
	N1     string         `json:"n1"`
	N2     string         `json:"n2"`
	W      int            `json:"w"`
	Score  string         `json:"score"`
	Ex     string         `json:"ex,omitempty"`
	Pt1    float64        `json:"pt1"`
	Pt2    float64        `json:"pt2"`
	Nomi   map[int]string `json:"nomi,omitempty"`
}

type RankRow struct {
	Pos   int     `json:"pos"`
	ID    int     `json:"id"`
	Nome  string  `json:"nome"`
	Class string  `json:"class"`
	Pt12  float64 `json:"pt12"`
	PtTot float64 `json:"ptTot"`
	G     int     `json:"g"`
	V     int     `json:"v"`
	P     int     `json:"p"`
	SV    int     `json:"sv"`
	SP    int     `json:"sp"`
	NC    bool    `json:"nc"`
}

var (
	circoloPath string
	internalPts = map[int]float64{} // punti degli ultimi mesi dei giocatori 4.NC, usati come spareggio nei sorteggi
)

func round1(x float64) float64 { return math.Round(x*10) / 10 }

// matchPoints: sistema "stile TennisCall" ricostruito dalle regole pubbliche
// (vincitore > perdente, conta il punteggio, W.O. = 0 punti).
//   vincitore = Vittoria * (0,5 + 0,5 * quota game vinti)
//   perdente  = Sconfitta * quota game vinti
func matchPoints(sets []SetRes, format string, w int, ex string, pr PuntiParams) (float64, float64) {
	if ex == "wo" {
		return 0, 0
	}
	ga, gb := gamesOf(sets, format)
	shareA := 0.5
	if ga+gb > 0 {
		shareA = float64(ga) / float64(ga+gb)
	}
	shareB := 1 - shareA
	if w == 1 {
		return round1(pr.Vittoria * (0.5 + 0.5*shareA)), round1(pr.Sconfitta * shareB)
	}
	return round1(pr.Sconfitta * shareA), round1(pr.Vittoria * (0.5 + 0.5*shareB))
}

func memberIDs(p *Player) []int {
	if len(p.Membri) > 0 {
		return p.Membri
	}
	return []int{p.ID}
}

func labelOf(p *Player) string {
	full, short := p.display()
	if len(p.Membri) > 0 {
		return full
	}
	return short
}

func syncLedger() {
	old := map[string]LedgerRow{}
	var keep []LedgerRow
	for _, r := range st.Ledger {
		if r.Torneo == st.TorneoID {
			old[r.Key] = r
		} else {
			keep = append(keep, r)
		}
	}
	var cur []LedgerRow
	add := func(key, cat, turno, format string, m *Match) {
		if m.A <= 0 || m.B <= 0 || m.W <= 0 {
			return
		}
		pa, pb := findAny(m.A), findAny(m.B)
		if pa == nil || pb == nil {
			return
		}
		sets := matchSets(m)
		r := LedgerRow{Key: key, Torneo: st.TorneoID, Titolo: st.Title, Cat: cat, Turno: turno, Score: m.Score, Ex: m.Ex}
		r.P1, r.P2 = memberIDs(pa), memberIDs(pb)
		r.N1, r.N2 = labelOf(pa), labelOf(pb)
		r.Nomi = map[int]string{}
		for _, id := range append(append([]int(nil), r.P1...), r.P2...) {
			if p := findPlayer(id); p != nil {
				r.Nomi[id] = strings.TrimSpace(p.Cognome + " " + p.Nome)
			}
		}
		r.W = 2
		if m.W == m.A {
			r.W = 1
		}
		r.Data = m.Data
		if r.Data == "" {
			if o, ok := old[key]; ok && o.Score == m.Score && o.W == r.W && o.Data != "" {
				r.Data = o.Data
			} else {
				r.Data = todayISO()
			}
		}
		r.Pt1, r.Pt2 = matchPoints(sets, format, r.W, m.Ex, st.Params)
		cur = append(cur, r)
	}
	for _, k := range catKeys {
		if d := st.Draws[k]; d != nil {
			for r := range d.Rounds {
				for i := range d.Rounds[r] {
					add(fmt.Sprintf("%d|E|%s|%d|%d", st.TorneoID, k, r, i), k, roundName(d.Size>>uint(r)), d.Fmt, &d.Rounds[r][i])
				}
			}
		}
		if rr := st.RRs[k]; rr != nil {
			for i := range rr.Matches {
				x := &rr.Matches[i]
				add(fmt.Sprintf("%d|R|%s|%d", st.TorneoID, k, i), k, fmt.Sprintf("Girone %c, turno %d", 'A'+x.G, x.R), rr.Fmt, &x.Match)
			}
		}
	}
	st.Ledger = append(keep, cur...)
}

func isNCPlayer(p *Player) bool {
	return strings.TrimSpace(p.Classifica) == "" || rankValue(p.Classifica) >= 4.7 || p.Esito == "NON AGONISTA"
}

func computeRanks() {
	cut := time.Now().AddDate(0, -st.Params.Mesi, 0).Format("2006-01-02")
	agg := map[int]*RankRow{}
	get := func(id int, fallback string) *RankRow {
		if r, ok := agg[id]; ok {
			return r
		}
		r := &RankRow{ID: id, Nome: fallback}
		if p := findPlayer(id); p != nil {
			r.Nome = strings.TrimSpace(p.Cognome + " " + p.Nome)
			r.Class = p.Classifica
			r.NC = isNCPlayer(p)
		}
		agg[id] = r
		return r
	}
	for _, lr := range st.Ledger {
		if lr.Ex == "wo" {
			continue // partita non giocata: nessun punto e nessuna statistica
		}
		sets := parseScore(lr.Score)
		sa, sb := setsWon(sets)
		for side := 1; side <= 2; side++ {
			ids, pts, sv, sp := lr.P1, lr.Pt1, sa, sb
			if side == 2 {
				ids, pts, sv, sp = lr.P2, lr.Pt2, sb, sa
			}
			for _, id := range ids {
				r := get(id, lr.Nomi[id])
				r.PtTot += pts
				if lr.Data >= cut {
					r.Pt12 += pts
				}
				r.G++
				if lr.W == side {
					r.V++
				} else {
					r.P++
				}
				r.SV += sv
				r.SP += sp
			}
		}
	}
	for _, p := range st.Players {
		if p.Iscritto && isNCPlayer(&p) {
			get(p.ID, "")
		}
	}
	var all []RankRow
	for _, r := range agg {
		r.Pt12, r.PtTot = round1(r.Pt12), round1(r.PtTot)
		all = append(all, *r)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Pt12 != b.Pt12 {
			return a.Pt12 > b.Pt12
		}
		if a.PtTot != b.PtTot {
			return a.PtTot > b.PtTot
		}
		if a.V != b.V {
			return a.V > b.V
		}
		return a.Nome < b.Nome
	})
	st.Circolo, st.Interna = nil, nil
	internalPts = map[int]float64{}
	for _, r := range all {
		if r.G > 0 {
			r.Pos = len(st.Circolo) + 1
			st.Circolo = append(st.Circolo, r)
		}
		if r.NC {
			r.Pos = len(st.Interna) + 1
			st.Interna = append(st.Interna, r)
			internalPts[r.ID] = r.Pt12
		}
	}
}

// refresh ricalcola tutto cio' che deriva dai risultati. Va chiamata con il lock `mu` gia' acquisito.
func refresh() {
	if st.Draws == nil {
		st.Draws = map[string]*Draw{}
	}
	if st.RRs == nil {
		st.RRs = map[string]*RR{}
	}
	if st.Params.Mesi <= 0 {
		st.Params.Mesi = 12
	}
	if st.Params.Vittoria <= 0 {
		st.Params.Vittoria = 10
	}
	if st.Params.Sconfitta < 0 {
		st.Params.Sconfitta = 0
	}
	st.RRStand = map[string][][]Stand{}
	for k, rr := range st.RRs {
		st.RRStand[k] = rrStandings(rr)
	}
	syncLedger()
	computeRanks()
}

type CircoloFile struct {
	Params     PuntiParams `json:"params"`
	TorneoID   int         `json:"torneoId"`
	Aggiornato string      `json:"aggiornato"`
	Ledger     []LedgerRow `json:"ledger"`
	Circolo    []RankRow   `json:"classificaCircolo"`
	Interna    []RankRow   `json:"classificaInterna"`
}

func writeCircolo() {
	if circoloPath == "" {
		return
	}
	writeJSONFile(circoloPath, &CircoloFile{Params: st.Params, TorneoID: st.TorneoID, Aggiornato: nowStamp(),
		Ledger: st.Ledger, Circolo: st.Circolo, Interna: st.Interna})
}

func loadCircolo() {
	st.Params, st.TorneoID, st.Ledger = defaultParams(), 0, nil
	if circoloPath == "" {
		return
	}
	b, err := os.ReadFile(circoloPath)
	if err != nil {
		return
	}
	var f CircoloFile
	if json.Unmarshal(b, &f) != nil {
		return
	}
	if f.Params != (PuntiParams{}) {
		st.Params = f.Params
	}
	st.TorneoID, st.Ledger = f.TorneoID, f.Ledger
}

// ---------- backup e ripristino ----------

func backupZip() ([]byte, error) {
	save()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range []string{statePath, dbPath, circoloPath, filepath.Join(dataDir, "tenniscall.csv")} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		w, err := zw.Create(filepath.Base(p))
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(b); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func restoreZip(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errors.New("il file non e' un backup valido (serve il .zip scaricato con \"Scarica backup\")")
	}
	allowed := map[string]bool{
		filepath.Base(statePath): true, filepath.Base(dbPath): true,
		filepath.Base(circoloPath): true, "tenniscall.csv": true,
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || !allowed[name] {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 20<<20))
		rc.Close()
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".json") && !json.Valid(b) {
			return fmt.Errorf("il file %s del backup e' danneggiato", name)
		}
		files[name] = b
	}
	if files[filepath.Base(statePath)] == nil && files[filepath.Base(dbPath)] == nil {
		return errors.New("nel file non ci sono i dati del torneo: non sembra un backup di questo programma")
	}
	if cur, err := backupZip(); err == nil { // copia di sicurezza dello stato attuale
		os.WriteFile(filepath.Join(dataDir, "prima_del_ripristino_"+time.Now().Format("20060102_150405")+".zip"), cur, 0644)
	}
	for name, b := range files {
		path := filepath.Join(dataDir, name)
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, b, 0644); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}
	loadState()
	st.ServerMode = serverMode
	return nil
}

// ---------- pagina pubblica ----------

func isPublicPath(p string) bool {
	return p == "/pubblico" || p == "/pubblico/" || p == "/api/pub" || p == "/logo.png"
}

type pubCat struct {
	Key   string      `json:"key"`
	Nome  string      `json:"nome"`
	Draw  *Draw       `json:"draw,omitempty"`
	RR    *RR         `json:"rr,omitempty"`
	Stand [][]Stand   `json:"stand,omitempty"`
}

type pubData struct {
	Title      string         `json:"title"`
	Date       string         `json:"date"`
	Luogo      string         `json:"luogo"`
	Aggiornato string         `json:"aggiornato"`
	Mesi       int            `json:"mesi"`
	Nomi       map[int]string `json:"nomi"`
	Rank       map[int]string `json:"rank"`
	Cats       []pubCat       `json:"cats"`
	Circolo    []RankRow      `json:"circolo"`
	Interna    []RankRow      `json:"interna"`
}

func buildPub() pubData {
	pd := pubData{Title: st.Title, Date: st.Date, Luogo: st.Luogo, Aggiornato: nowStamp(), Mesi: st.Params.Mesi,
		Nomi: map[int]string{}, Rank: map[int]string{}, Circolo: st.Circolo, Interna: st.Interna}
	use := func(id int) {
		if id <= 0 {
			return
		}
		if p := findAny(id); p != nil {
			pd.Nomi[id] = strings.TrimSpace(p.Cognome + " " + p.Nome)
			pd.Rank[id] = strings.TrimSpace(p.Classifica)
		}
	}
	for _, k := range catKeys {
		c := pubCat{Key: k, Nome: catTitle(k)}
		if d := st.Draws[k]; d != nil {
			c.Draw = d
			for _, m := range d.Rounds[0] {
				use(m.A)
				use(m.B)
			}
		} else if rr := st.RRs[k]; rr != nil {
			c.RR = rr
			c.Stand = st.RRStand[k]
			for _, g := range rr.Groups {
				for _, id := range g {
					use(id)
				}
			}
		} else {
			continue
		}
		pd.Cats = append(pd.Cats, c)
	}
	return pd
}

// ---------- API ----------

func registerExtra(mux *http.ServeMux) {
	// risultato con punteggio per set
	mux.HandleFunc("/api/setscore", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			Kind  string   `json:"kind"` // "E" eliminazione, "R" girone
			Key   string   `json:"key"`
			Round int      `json:"round"`
			Match int      `json:"match"`
			Sets  []SetRes `json:"sets"`
			Ex    string   `json:"ex"`
			Perde string   `json:"perde"`
			Clear bool     `json:"clear"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		if b.Kind == "R" {
			rr := st.RRs[b.Key]
			if rr == nil || b.Match < 0 || b.Match >= len(rr.Matches) {
				return nil, errors.New("partita non trovata")
			}
			m := &rr.Matches[b.Match].Match
			if b.Clear {
				m.reset()
				return &st, nil
			}
			if err := applyResult(m, rr.Fmt, b.Sets, b.Ex, b.Perde); err != nil {
				return nil, err
			}
			return &st, nil
		}
		d := st.Draws[b.Key]
		if d == nil || b.Round < 0 || b.Round >= len(d.Rounds) || b.Match < 0 || b.Match >= len(d.Rounds[b.Round]) {
			return nil, errors.New("partita non trovata")
		}
		m := &d.Rounds[b.Round][b.Match]
		if b.Clear {
			m.reset()
		} else if err := applyResult(m, d.Fmt, b.Sets, b.Ex, b.Perde); err != nil {
			return nil, err
		}
		recompute(d)
		return &st, nil
	}))

	// gironi all'italiana
	mux.HandleFunc("/api/rr", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			Key    string `json:"key"`
			Groups int    `json:"groups"`
			Fmt    string `json:"fmt"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		pl, err := playersForKey(b.Key)
		if err != nil {
			return nil, err
		}
		rr := makeRR(pl, b.Groups, rand.New(rand.NewSource(time.Now().UnixNano())))
		rr.Fmt = normFmt(b.Fmt)
		st.RRs[b.Key] = rr
		delete(st.Draws, b.Key)
		return &st, nil
	}))
	mux.HandleFunc("/api/rr/delete", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			Key string `json:"key"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		delete(st.RRs, b.Key)
		return &st, nil
	}))

	// coppie di doppio
	mux.HandleFunc("/api/coppia/add", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			Cat string `json:"cat"`
			A   int    `json:"a"`
			B   int    `json:"b"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		if !isDoubles(b.Cat) || !validCat(b.Cat) {
			return nil, errors.New("categoria di doppio non valida")
		}
		pa, pb := findPlayer(b.A), findPlayer(b.B)
		if pa == nil || pb == nil || b.A == b.B {
			return nil, errors.New("scegli due giocatori diversi")
		}
		switch b.Cat {
		case "DM":
			if pa.Sesso != "M" || pb.Sesso != "M" {
				return nil, errors.New("nel doppio maschile servono due uomini")
			}
		case "DF":
			if pa.Sesso != "F" || pb.Sesso != "F" {
				return nil, errors.New("nel doppio femminile servono due donne")
			}
		case "DX":
			if pa.Sesso == pb.Sesso {
				return nil, errors.New("nel doppio misto servono un uomo e una donna")
			}
		}
		if st.Draws[b.Cat] != nil || st.RRs[b.Cat] != nil {
			return nil, errors.New("c'e' gia' un tabellone per questo doppio: eliminalo prima di cambiare le coppie")
		}
		for _, c := range st.Coppie {
			if c.Cat != b.Cat {
				continue
			}
			for _, mm := range c.Membri {
				if mm == b.A || mm == b.B {
					return nil, errors.New("uno dei due giocatori e' gia' in una coppia di questa categoria")
				}
			}
		}
		st.Coppie = append(st.Coppie, Player{
			ID: st.NextID, Cognome: strings.TrimSpace(pa.Cognome) + " / " + strings.TrimSpace(pb.Cognome),
			Classifica: strings.TrimSpace(pa.Classifica) + " / " + strings.TrimSpace(pb.Classifica),
			Forza:      (pa.Forza + pb.Forza) / 2, Esito: "COPPIA", Iscritto: true, Cat: b.Cat, Membri: []int{b.A, b.B},
		})
		st.NextID++
		return &st, nil
	}))
	mux.HandleFunc("/api/coppia/delete", api(func(r *http.Request) (interface{}, error) {
		var b struct {
			ID int `json:"id"`
		}
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		for _, d := range st.Draws {
			for _, m := range d.Rounds[0] {
				if m.A == b.ID || m.B == b.ID {
					return nil, errors.New("la coppia e' nel tabellone: elimina prima il tabellone")
				}
			}
		}
		for _, rr := range st.RRs {
			for _, g := range rr.Groups {
				for _, id := range g {
					if id == b.ID {
						return nil, errors.New("la coppia e' in un girone: elimina prima il girone")
					}
				}
			}
		}
		for i := range st.Coppie {
			if st.Coppie[i].ID == b.ID {
				st.Coppie = append(st.Coppie[:i], st.Coppie[i+1:]...)
				break
			}
		}
		return &st, nil
	}))

	// classifica di circolo
	mux.HandleFunc("/api/params", api(func(r *http.Request) (interface{}, error) {
		var b PuntiParams
		if err := readBody(r, &b); err != nil {
			return nil, err
		}
		if b.Vittoria <= 0 || b.Sconfitta < 0 || b.Sconfitta >= b.Vittoria || b.Mesi < 1 || b.Mesi > 60 {
			return nil, errors.New("valori non validi: la vittoria deve valere piu' della sconfitta e i mesi essere tra 1 e 60")
		}
		st.Params = b
		return &st, nil
	}))
	mux.HandleFunc("/api/torneo/chiudi", api(func(r *http.Request) (interface{}, error) {
		refresh() // i punti del torneo vengono congelati nello storico
		st.TorneoID++
		st.Draws = map[string]*Draw{}
		st.RRs = map[string]*RR{}
		st.Coppie = nil
		return &st, nil
	}))

	// backup / ripristino
	mux.HandleFunc("/api/backup", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, err := backupZip()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="backup_tc_salemi_`+time.Now().Format("2006-01-02")+`.zip"`)
		w.Write(b)
	})
	mux.HandleFunc("/api/restore", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "metodo non consentito", http.StatusMethodNotAllowed)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 40<<20))
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if err := restoreZip(data); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, &st)
	})

	// pagina pubblica di sola lettura (senza password)
	mux.HandleFunc("/pubblico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Write([]byte(pubHTML))
	})
	mux.HandleFunc("/pubblico/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/pubblico", http.StatusFound)
	})
	mux.HandleFunc("/api/pub", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pd := buildPub()
		b, _ := json.Marshal(pd)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Write(b)
	})
}
