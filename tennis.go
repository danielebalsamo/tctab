package main

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	reForm  = regexp.MustCompile(`(?is)<form\b([^>]*)>(.*?)</form>`)
	reInput = regexp.MustCompile(`(?is)<(input|button)\b([^>]*)>`)
	reAttr  = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	reTags  = regexp.MustCompile(`(?is)<script.*?</script>|<style.*?</style>|<[^>]+>`)
	reSpace = regexp.MustCompile(`\s+`)
)

func htmlAttrs(s string) map[string]string {
	m := map[string]string{}
	for _, a := range reAttr.FindAllStringSubmatch(s, -1) {
		v := a[2]
		if v == "" {
			v = a[3]
		}
		if v == "" {
			v = a[4]
		}
		m[strings.ToLower(a[1])] = html.UnescapeString(v)
	}
	return m
}

type loginForm struct {
	action, method       string
	userField, passField string
	fixed                [][2]string // campi nascosti (token CSRF ecc.) e pulsante di invio
}

// findLoginForm cerca nella pagina un modulo con un campo password.
func findLoginForm(page string, base *url.URL) (*loginForm, bool) {
	for _, fm := range reForm.FindAllStringSubmatch(page, -1) {
		fa := htmlAttrs(fm[1])
		lf := &loginForm{method: strings.ToUpper(strings.TrimSpace(fa["method"]))}
		if lf.method == "" {
			lf.method = "GET"
		}
		hasPass, submitAdded := false, false
		var lastText string
		for _, in := range reInput.FindAllStringSubmatch(fm[2], -1) {
			tag := strings.ToLower(in[1])
			a := htmlAttrs(in[2])
			name := a["name"]
			typ := strings.ToLower(a["type"])
			if tag == "button" && typ == "" {
				typ = "submit"
			}
			if tag == "input" && typ == "" {
				typ = "text"
			}
			switch typ {
			case "password":
				if !hasPass {
					hasPass = true
					lf.passField = name
				}
			case "hidden":
				if name != "" {
					lf.fixed = append(lf.fixed, [2]string{name, a["value"]})
				}
			case "text", "email", "tel":
				if !hasPass && name != "" {
					lastText = name
				}
			case "submit", "button":
				if name != "" && !submitAdded && typ == "submit" {
					lf.fixed = append(lf.fixed, [2]string{name, a["value"]})
					submitAdded = true
				}
			case "checkbox":
				if name != "" && strings.Contains(strings.ToLower(name), "remember") {
					v := a["value"]
					if v == "" {
						v = "1"
					}
					lf.fixed = append(lf.fixed, [2]string{name, v})
				}
			}
		}
		if !hasPass || lf.passField == "" {
			continue
		}
		lf.userField = lastText
		if lf.userField == "" {
			continue
		}
		act := strings.TrimSpace(fa["action"])
		target := base
		if act != "" {
			if u, err := base.Parse(act); err == nil {
				target = u
			}
		}
		lf.action = target.String()
		return lf, true
	}
	return nil, false
}

func visibleText(b []byte, max int) string {
	s := html.UnescapeString(reTags.ReplaceAllString(string(b), " "))
	s = strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max]) + "..."
	}
	return s
}

func looksBlocked(resp *http.Response, body []byte) bool {
	if resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode == 503 {
		return true
	}
	l := strings.ToLower(string(body[:min(len(body), 4000)]))
	return strings.Contains(l, "captcha") || strings.Contains(l, "just a moment") || strings.Contains(l, "attention required") || strings.Contains(l, "cf-browser-verification")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func isCSVResponse(resp *http.Response, body []byte) bool {
	if resp.StatusCode != 200 || len(body) == 0 {
		return false
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	cd := strings.ToLower(resp.Header.Get("Content-Disposition"))
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "json") {
		return false
	}
	return strings.Contains(ct, "csv") || strings.Contains(cd, "attachment") || strings.Contains(ct, "octet-stream") || strings.Contains(ct, "text/plain")
}

// tcFetchExport accede a TennisCall con le credenziali ricevute, scarica il CSV
// dell'esportazione e lo restituisce in memoria. Le credenziali non vengono salvate.
func tcFetchExport(exportURL, user, pass string) ([]byte, []string, error) {
	var steps []string
	logf := func(f string, a ...interface{}) { steps = append(steps, fmt.Sprintf(f, a...)) }
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, Timeout: 40 * time.Second}
	do := func(req *http.Request) (*http.Response, []byte, error) {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/csv,*/*;q=0.8")
		req.Header.Set("Accept-Language", "it-IT,it;q=0.9,en;q=0.8")
		resp, err := cl.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		return resp, b, nil
	}
	describe := func(resp *http.Response, b []byte) string {
		return fmt.Sprintf("HTTP %d, %s, %d byte, indirizzo finale %s", resp.StatusCode, strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0], len(b), resp.Request.URL.Host+resp.Request.URL.Path)
	}
	// validazione del CSV: deve contenere un elenco di persone
	accept := func(b []byte) ([]byte, error) {
		rows, err := readCSVRows(b)
		if err == nil {
			_, err = importRows(rows)
		}
		if err != nil {
			return nil, fmt.Errorf("il file scaricato non contiene un elenco di persone riconoscibile (%v). Inizio del file: %s", err, visibleText(b, 120))
		}
		return b, nil
	}

	req, _ := http.NewRequest("GET", exportURL, nil)
	resp, body, err := do(req)
	if err != nil {
		return nil, steps, fmt.Errorf("connessione a TennisCall non riuscita: %v", err)
	}
	logf("1) apro l'indirizzo di esportazione: %s", describe(resp, body))
	if isCSVResponse(resp, body) {
		b, err := accept(body)
		return b, steps, err
	}
	if looksBlocked(resp, body) {
		return nil, steps, fmt.Errorf("il sito ha bloccato la richiesta automatica (protezione anti-robot, HTTP %d): non e' possibile accedere dal programma. Usa il metodo con il browser", resp.StatusCode)
	}
	lf, ok := findLoginForm(string(body), resp.Request.URL)
	if !ok {
		return nil, steps, fmt.Errorf("non riconosco la pagina di accesso di TennisCall (forse usa JavaScript o un controllo aggiuntivo). Contenuto: %s", visibleText(body, 160))
	}
	logf("2) trovato il modulo di accesso (campi: %s / %s)", lf.userField, lf.passField)
	vals := url.Values{}
	for _, f := range lf.fixed {
		vals.Add(f[0], f[1])
	}
	vals.Set(lf.userField, user)
	vals.Set(lf.passField, pass)
	var preq *http.Request
	if lf.method == "POST" {
		preq, _ = http.NewRequest("POST", lf.action, strings.NewReader(vals.Encode()))
		preq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		u, _ := url.Parse(lf.action)
		u.RawQuery = vals.Encode()
		preq, _ = http.NewRequest("GET", u.String(), nil)
	}
	preq.Header.Set("Referer", resp.Request.URL.String())
	preq.Header.Set("Origin", resp.Request.URL.Scheme+"://"+resp.Request.URL.Host)
	resp2, body2, err := do(preq)
	if err != nil {
		return nil, steps, fmt.Errorf("invio delle credenziali non riuscito: %v", err)
	}
	logf("3) invio le credenziali: %s", describe(resp2, body2))
	if isCSVResponse(resp2, body2) {
		b, err := accept(body2)
		return b, steps, err
	}
	if looksBlocked(resp2, body2) {
		return nil, steps, fmt.Errorf("il sito ha bloccato l'accesso automatico (HTTP %d, protezione anti-robot o controllo aggiuntivo)", resp2.StatusCode)
	}
	if _, again := findLoginForm(string(body2), resp2.Request.URL); again {
		return nil, steps, fmt.Errorf("accesso rifiutato: credenziali non corrette (o richiesto un controllo aggiuntivo). Il sito dice: %s", visibleText(body2, 160))
	}
	req3, _ := http.NewRequest("GET", exportURL, nil)
	resp3, body3, err := do(req3)
	if err != nil {
		return nil, steps, fmt.Errorf("scaricamento non riuscito: %v", err)
	}
	logf("4) riapro l'esportazione dopo l'accesso: %s", describe(resp3, body3))
	if isCSVResponse(resp3, body3) {
		b, err := accept(body3)
		return b, steps, err
	}
	return nil, steps, fmt.Errorf("accesso eseguito, ma l'esportazione non ha restituito un file CSV (HTTP %d, %s). Risposta: %s",
		resp3.StatusCode, strings.SplitN(resp3.Header.Get("Content-Type"), ";", 2)[0], visibleText(body3, 160))
}
