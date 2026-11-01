package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const defaultFitpURL = "https://dp-myfit-test-function-v2.azurewebsites.net/api/v1/tesserati/list"

// ---------- utilita' nomi ----------

func tokens(s string) []string {
	s = accents.Replace(strings.ToLower(s))
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func nameKey(parts ...string) string {
	t := tokens(strings.Join(parts, " "))
	sort.Strings(t)
	return strings.Join(t, "|")
}

func jval(m map[string]interface{}, k string) string {
	switch v := m[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// ---------- classifica con storico ----------

func setRank(p *Player, nr, fonte string) {
	nr = strings.TrimSpace(nr)
	if nr != p.Classifica {
		p.Storico = append(p.Storico, Cambio{Data: nowStamp(), Da: p.Classifica, A: nr, Fonte: fonte})
		if len(p.Storico) > 30 {
			p.Storico = p.Storico[len(p.Storico)-30:]
		}
		p.Classifica = nr
	}
	p.Forza = rankValue(p.Classifica)
}

// ---------- chiamata al portale ----------

func fitpSearch(cl *http.Client, url, text string, rows, attempts int) ([]map[string]interface{}, error) {
	body := map[string]interface{}{
		"id_disciplina": 4332, "id_provincia": nil, "id_regione": nil, "id_gruppo_rank": nil,
		"id_categoria_rank": nil, "id_categoria_eta": nil, "sesso": nil,
		"freetext": text, "rowstoskip": 0, "fetchrows": rows, "sortcolumn": "cognome", "sortorder": "asc",
	}
	b, _ := json.Marshal(body)
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Origin", "https://www.fitp.it")
		req.Header.Set("Referer", "https://www.fitp.it/")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120 Safari/537.36")
		resp, err := cl.Do(req)
		if err == nil {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				var r struct {
					Giocatori []map[string]interface{} `json:"giocatori"`
				}
				if e := json.Unmarshal(data, &r); e != nil {
					last = e
				} else {
					return r.Giocatori, nil
				}
			} else {
				last = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		} else {
			last = err
		}
		if attempt < attempts {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil, last
}

type fitpRes struct {
	Status string // found | none | verify | error
	Rank   string
	Cat    string
	Prov   string
	Note   string
}

func fitpDescribe(m map[string]interface{}) string {
	return fmt.Sprintf("%s %s (%s) classifica %s", jval(m, "c"), jval(m, "n"), jval(m, "pro"), jval(m, "gr"))
}

func fitpList(c []map[string]interface{}, max int) string {
	var s []string
	for i, m := range c {
		if i >= max {
			s = append(s, fmt.Sprintf("... altri %d", len(c)-max))
			break
		}
		s = append(s, fitpDescribe(m))
	}
	return strings.Join(s, " | ")
}

func fitpLookup(cl *http.Client, url, first, last, prov string, attempts int) fitpRes {
	res, err := fitpSearch(cl, url, first+" "+last, 50, attempts)
	if err != nil {
		return fitpRes{Status: "error", Note: err.Error()}
	}
	want := nameKey(first, last)
	var exact []map[string]interface{}
	for _, m := range res {
		if nameKey(jval(m, "c"), jval(m, "n")) == want {
			exact = append(exact, m)
		}
	}
	if len(exact) == 0 {
		var cands []map[string]interface{}
		if len(res) > 0 {
			cands = res
		} else {
			res2, err := fitpSearch(cl, url, last, 50, attempts)
			if err != nil {
				return fitpRes{Status: "error", Note: err.Error()}
			}
			sur := tokens(last)
			for _, m := range res2 {
				full := map[string]bool{}
				for _, t := range tokens(jval(m, "c") + " " + jval(m, "n")) {
					full[t] = true
				}
				ok := true
				for _, t := range sur {
					if !full[t] {
						ok = false
					}
				}
				if ok {
					cands = append(cands, m)
				}
			}
		}
		if len(cands) == 0 {
			return fitpRes{Status: "none", Note: "Nessun risultato nel portale"}
		}
		sort.SliceStable(cands, func(i, j int) bool {
			return strings.EqualFold(jval(cands[i], "pro"), prov) && !strings.EqualFold(jval(cands[j], "pro"), prov)
		})
		return fitpRes{Status: "verify", Note: "Nessuna corrispondenza esatta. Simili: " + fitpList(cands, 6)}
	}
	pick := exact
	if len(exact) > 1 {
		var local []map[string]interface{}
		for _, m := range exact {
			if strings.EqualFold(jval(m, "pro"), prov) {
				local = append(local, m)
			}
		}
		if len(local) == 1 {
			pick = local
		} else {
			return fitpRes{Status: "verify", Note: "Omonimi: " + fitpList(exact, 6)}
		}
	}
	m := pick[0]
	r := fitpRes{Status: "found", Rank: jval(m, "gr"), Cat: jval(m, "cr"), Prov: jval(m, "pro")}
	if len(exact) > 1 {
		r.Note = "Scelto omonimo della provincia " + prov
	} else if !strings.EqualFold(r.Prov, prov) {
		r.Note = "Provincia diversa da " + prov + ": verificare che sia la stessa persona"
	}
	if r.Rank == "" || r.Rank == "-" {
		r.Rank = ""
		r.Note = strings.TrimSpace(r.Note + " Classifica non indicata dal portale")
	}
	return r
}

// applyFitp aggiorna il giocatore in base all'esito. Non sovrascrive mai
// una classifica valida con un dato incerto (errore di rete, giocatore non piu' trovato).
func applyFitp(p *Player, r fitpRes) {
	p.Aggiornato = nowStamp()
	switch r.Status {
	case "found":
		if r.Rank != "" {
			setRank(p, r.Rank, "portale")
		}
		p.Categoria, p.Provincia = r.Cat, r.Prov
		p.Esito, p.Note = "TROVATO", r.Note
	case "none":
		if reNum.MatchString(strings.TrimSpace(p.Classifica)) && rankValue(p.Classifica) < 4.7 {
			p.Esito = "DA VERIFICARE"
			p.Note = "Il portale non trova piu' questo giocatore (prima: " + p.Classifica + "): classifica non modificata"
		} else {
			setRank(p, "4.NC", "portale")
			p.Categoria, p.Provincia = "", ""
			p.Esito, p.Note = "NON AGONISTA", r.Note+": assegnata 4.NC"
		}
	case "verify":
		p.Esito, p.Note = "DA VERIFICARE", r.Note
	default:
		prev := strings.TrimSpace(p.Classifica)
		setRank(p, "4.NC", "portale (errore)")
		p.Esito = "4.NC (ERRORE RETE)"
		p.Note = "Errore di connessione (" + r.Note + "): assegnata 4.NC"
		if prev != "" && prev != "4.NC" {
			p.Note += " (classifica precedente: " + prev + ", visibile nello storico)"
		}
	}
}

// ---------- aggiornamento in background ----------

type fitpJob struct {
	Running   bool   `json:"running"`
	Cancel    bool   `json:"cancel"`
	Total     int    `json:"total"`
	Done      int    `json:"done"`
	Current   string `json:"current"`
	Found     int    `json:"found"`
	None      int    `json:"none"`
	Verify    int    `json:"verify"`
	Errors    int    `json:"errors"`
	Changed   int    `json:"changed"`
	Started   string `json:"started"`
	Finished  string `json:"finished"`
	Message   string `json:"message"`
	LastError string `json:"lastError"`
}

var fjob fitpJob

// startUpdate va chiamata con il lock `mu` gia' acquisito.
func startUpdate(ids []int, onlyIscritti bool) error {
	if fjob.Running {
		return fmt.Errorf("un aggiornamento e' gia' in corso")
	}
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	type target struct {
		id          int
		first, last string
	}
	var ts []target
	for _, p := range st.Players {
		if len(want) > 0 && !want[p.ID] {
			continue
		}
		if len(want) == 0 && onlyIscritti && !p.Iscritto {
			continue
		}
		ts = append(ts, target{p.ID, p.Nome, p.Cognome})
	}
	if len(ts) == 0 {
		return fmt.Errorf("nessun giocatore da aggiornare")
	}
	url := strings.TrimSpace(st.FitpURL)
	if url == "" {
		url = defaultFitpURL
	}
	full := len(want) == 0 && !onlyIscritti
	fjob = fitpJob{Running: true, Total: len(ts), Started: nowStamp()}
	go func() {
		cl := &http.Client{Timeout: 15 * time.Second}
		consec := 0 // solo per velocizzare quando il portale non risponde: non interrompe mai
		for _, t := range ts {
			mu.Lock()
			if fjob.Cancel {
				fjob.Message = "Aggiornamento annullato"
				mu.Unlock()
				break
			}
			fjob.Current = strings.TrimSpace(t.last + " " + t.first)
			mu.Unlock()

			attempts := 2
			if consec >= 3 {
				attempts = 1
			}
			r := fitpLookup(cl, url, t.first, t.last, "TP", attempts)

			mu.Lock()
			esito := "ERRORE RETE"
			if p := findPlayer(t.id); p != nil {
				old := p.Classifica
				applyFitp(p, r)
				esito = p.Esito
				if p.Classifica != old {
					fjob.Changed++
				}
			}
			switch esito {
			case "TROVATO":
				fjob.Found++
			case "NON AGONISTA":
				fjob.None++
			case "DA VERIFICARE":
				fjob.Verify++
			default:
				fjob.Errors++
			}
			fjob.Done++
			if fjob.Done%5 == 0 {
				save()
			}
			mu.Unlock()

			if r.Status == "error" {
				consec++
				mu.Lock()
				if fjob.LastError == "" {
					fjob.LastError = r.Note
				}
				mu.Unlock()
			} else {
				consec = 0
			}
			time.Sleep(300 * time.Millisecond)
		}
		mu.Lock()
		fjob.Running = false
		fjob.Current = ""
		fjob.Finished = nowStamp()
		if fjob.Message == "" && full {
			st.Aggiornamento = fjob.Finished
		}
		save()
		mu.Unlock()
	}()
	return nil
}

// ---------- diagnostica connessione ----------

type fitpProbeRes struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	Count  int    `json:"count"`
	Sample string `json:"sample"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}

func fitpProbe(url string) fitpProbeRes {
	res := fitpProbeRes{URL: url}
	body := map[string]interface{}{
		"id_disciplina": 4332, "id_provincia": nil, "id_regione": nil, "id_gruppo_rank": nil,
		"id_categoria_rank": nil, "id_categoria_eta": nil, "sesso": nil,
		"freetext": "Rossi", "rowstoskip": 0, "fetchrows": 5, "sortcolumn": "cognome", "sortorder": "asc",
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", "https://www.fitp.it")
	req.Header.Set("Referer", "https://www.fitp.it/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120 Safari/537.36")
	cl := &http.Client{Timeout: 20 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	res.Status = resp.StatusCode
	snip := string(data)
	if len(snip) > 400 {
		snip = snip[:400] + "..."
	}
	res.Body = snip
	var r struct {
		Giocatori []map[string]interface{} `json:"giocatori"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		if resp.StatusCode == 200 {
			res.Error = "risposta non riconosciuta: " + err.Error()
		}
		return res
	}
	res.Count = len(r.Giocatori)
	if len(r.Giocatori) > 0 {
		res.Sample = fitpDescribe(r.Giocatori[0])
	}
	return res
}
