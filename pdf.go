package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/png"
	"math"
	"strconv"
	"strings"
	"unicode"
)

var helv = []int{278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278, 556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556, 1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778, 667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556, 333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556, 556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584}

func foldRune(r rune) rune {
	if r < 128 {
		return r
	}
	s := accents.Replace(strings.ToLower(string(r)))
	rs := []rune(s)
	if len(rs) == 1 && rs[0] < 128 {
		if unicode.IsUpper(r) {
			return unicode.ToUpper(rs[0])
		}
		return rs[0]
	}
	return 'n'
}

// isEmojiRune riconosce emoji, simboli e caratteri di servizio (selettori, giunzioni) che i font
// standard del PDF non possono disegnare.
func isEmojiRune(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF,
		r >= 0x2190 && r <= 0x21FF,
		r >= 0x2300 && r <= 0x23FF,
		r >= 0x2460 && r <= 0x24FF,
		r >= 0x25A0 && r <= 0x27BF,
		r >= 0x2900 && r <= 0x297F,
		r >= 0x2B00 && r <= 0x2BFF,
		r >= 0xFE00 && r <= 0xFE0F,
		r >= 0xE0000 && r <= 0xE007F,
		r == 0x200D, r == 0x20E3, r == 0x3030, r == 0x303D, r == 0x3297, r == 0x3299:
		return true
	}
	return false
}

func textWidth(s string, size float64, bold bool) float64 {
	w := 0
	for _, r := range s {
		if isEmojiRune(r) {
			continue
		}
		r = foldRune(r)
		if r >= 32 && r <= 126 {
			w += helv[r-32]
		} else {
			w += 556
		}
	}
	k := 1.0
	if bold {
		k = 1.06
	}
	return float64(w) / 1000 * size * k
}

// toCP1252 converte il testo UTF-8 nella codifica WinAnsi usata dal PDF.
func toCP1252(s string) string {
	var b []byte
	for _, r := range s {
		switch {
		case r < 128:
			b = append(b, byte(r))
		case r >= 160 && r <= 255:
			b = append(b, byte(r))
		case r == '’':
			b = append(b, 0x92)
		case r == '‘':
			b = append(b, 0x91)
		case r == '“':
			b = append(b, 0x93)
		case r == '”':
			b = append(b, 0x94)
		case r == '–':
			b = append(b, 0x96)
		case r == '—':
			b = append(b, 0x97)
		case r == '€':
			b = append(b, 0x80)
		case r == '·' || r == '•':
			b = append(b, 0xB7)
		case isEmojiRune(r):
			// le emoji non esistono nei font del PDF: vengono omesse invece di diventare "?"
		default:
			b = append(b, '?')
		}
	}
	return string(b)
}

func pdfEsc(s string) string {
	s = toCP1252(s)
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "(", "\\(")
	s = strings.ReplaceAll(s, ")", "\\)")
	return s
}

type page struct {
	b    bytes.Buffer
	w, h float64
}

const (
	stNormal = iota
	stBold
	stItalic
)

func (p *page) line(x1, y1, x2, y2, lw, gray float64) {
	fmt.Fprintf(&p.b, "%.2f G %.2f w %.2f %.2f m %.2f %.2f l S\n", gray, lw, x1, p.h-y1, x2, p.h-y2)
}

func (p *page) rect(x, y, w, h, gray float64) {
	fmt.Fprintf(&p.b, "%.2f g %.2f %.2f %.2f %.2f re f\n", gray, x, p.h-y-h, w, h)
}

func (p *page) text(x, y, size float64, style int, gray float64, s string) {
	font := "F1"
	if style == stBold {
		font = "F2"
	} else if style == stItalic {
		font = "F3"
	}
	fmt.Fprintf(&p.b, "BT /%s %.2f Tf %.2f g %.2f %.2f Td (%s) Tj ET\n", font, size, gray, x, p.h-y, pdfEsc(s))
}

type pdfImg struct {
	W, H       int
	Data, Mask []byte
}

type pdfDoc struct {
	pages []*page
	logo  *pdfImg
	fav   *pdfImg
}

// curFav e' la favicon del circolo, disegnata accanto al nome nell'intestazione (impostata da buildPDF).
var curFav *pdfImg

func (p *page) favicon(x, y, w, h float64) {
	fmt.Fprintf(&p.b, "q %.2f 0 0 %.2f %.2f %.2f cm /Im2 Do Q\n", w, h, x, p.h-y-h)
}

func (p *page) image(x, y, w, h float64) {
	fmt.Fprintf(&p.b, "q %.2f 0 0 %.2f %.2f %.2f cm /Im1 Do Q\n", w, h, x, p.h-y-h)
}

func zlibBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// loadLogo legge un PNG (anche trasparente) e lo prepara per il PDF, riducendolo se molto grande.
func loadLogo(data []byte) (*pdfImg, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return prepareImage(img), nil
}

func prepareImage(img image.Image) *pdfImg {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	step := 1
	for sw/step > 500 || sh/step > 500 {
		step++
	}
	w, h := sw/step, sh/step
	if w < 1 || h < 1 {
		w, h, step = sw, sh, 1
	}
	rgb := make([]byte, 0, w*h*3)
	alpha := make([]byte, 0, w*h)
	hasAlpha := false
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, bl, a, n uint64
			for dy := 0; dy < step; dy++ {
				for dx := 0; dx < step; dx++ {
					cr, cg, cb, ca := img.At(b.Min.X+x*step+dx, b.Min.Y+y*step+dy).RGBA()
					r, g, bl, a, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca), n+1
				}
			}
			r, g, bl, a = r/n, g/n, bl/n, a/n
			if a > 0 {
				r, g, bl = r*0xffff/a, g*0xffff/a, bl*0xffff/a
			}
			if a < 0xffff {
				hasAlpha = true
			}
			rgb = append(rgb, byte(r>>8), byte(g>>8), byte(bl>>8))
			alpha = append(alpha, byte(a>>8))
		}
	}
	im := &pdfImg{W: w, H: h, Data: zlibBytes(rgb)}
	if hasAlpha {
		im.Mask = zlibBytes(alpha)
	}
	return im
}

func (d *pdfDoc) newPage() *page {
	p := &page{w: 842, h: 595}
	d.pages = append(d.pages, p)
	return p
}

func (d *pdfDoc) bytes() []byte {
	var out bytes.Buffer
	var offs []int
	out.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	obj := func(s string) {
		offs = append(offs, out.Len())
		out.WriteString(s)
	}
	var kids []string
	for i := range d.pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 6+2*i))
	}
	obj("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	obj(fmt.Sprintf("2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, " "), len(d.pages)))
	for i, f := range []string{"Helvetica", "Helvetica-Bold", "Helvetica-Oblique"} {
		obj(fmt.Sprintf("%d 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /%s /Encoding /WinAnsiEncoding >>\nendobj\n", 3+i, f))
	}
	logoID := 6 + 2*len(d.pages)
	favID := logoID
	if d.logo != nil {
		favID++
		if d.logo.Mask != nil {
			favID++
		}
	}
	for i, p := range d.pages {
		res := "/Font << /F1 3 0 R /F2 4 0 R /F3 5 0 R >>"
		xo := ""
		if d.logo != nil {
			xo += fmt.Sprintf(" /Im1 %d 0 R", logoID)
		}
		if d.fav != nil {
			xo += fmt.Sprintf(" /Im2 %d 0 R", favID)
		}
		if xo != "" {
			res += " /XObject <<" + xo + " >>"
		}
		obj(fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << %s >> /Contents %d 0 R >>\nendobj\n", 6+2*i, p.w, p.h, res, 7+2*i))
		obj(fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", 7+2*i, p.b.Len(), p.b.String()))
	}
	writeImg := func(id int, im *pdfImg) {
		sm := ""
		if im.Mask != nil {
			sm = fmt.Sprintf(" /SMask %d 0 R", id+1)
		}
		obj(fmt.Sprintf("%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d%s >>\nstream\n", id, im.W, im.H, len(im.Data), sm))
		out.Write(im.Data)
		out.WriteString("\nendstream\nendobj\n")
		if im.Mask != nil {
			obj(fmt.Sprintf("%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", id+1, im.W, im.H, len(im.Mask)))
			out.Write(im.Mask)
			out.WriteString("\nendstream\nendobj\n")
		}
	}
	if d.logo != nil {
		writeImg(logoID, d.logo)
	}
	if d.fav != nil {
		writeImg(favID, d.fav)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return out.Bytes()
}

func roundName(playersInRound int) string {
	switch playersInRound {
	case 2:
		return "Finale"
	case 4:
		return "Semifinali"
	case 8:
		return "Quarti di finale"
	case 16:
		return "Ottavi di finale"
	case 32:
		return "Sedicesimi"
	case 64:
		return "Trentaduesimi"
	}
	return fmt.Sprintf("Turno da %d", playersInRound)
}

func (p *Player) display() (full, short string) {
	full = strings.TrimSpace(p.Cognome + " " + p.Nome)
	n := []rune(strings.TrimSpace(p.Nome))
	if len(n) > 0 {
		short = strings.TrimSpace(p.Cognome + " " + string(n[0]) + ".")
	} else {
		short = full
	}
	return
}

func fitText(cands []string, maxW, size float64, bold bool) string {
	for _, c := range cands {
		if textWidth(c, size, bold) <= maxW {
			return c
		}
	}
	s := []rune(cands[len(cands)-1])
	for len(s) > 1 && textWidth(string(s)+"...", size, bold) > maxW {
		s = s[:len(s)-1]
	}
	return string(s) + "..."
}

func sexTitle(sex string) string {
	return catTitle(sex)
}

func drawBracket(pg *page, sex string, d *Draw, pl map[int]*Player, title, date, luogo, stamp string, logo *pdfImg) {
	// intestazione
	pg.rect(0, 0, pg.w, 58, 0.14)
	tx := 28.0
	if logo != nil {
		lh := 44.0
		lw := lh * float64(logo.W) / float64(logo.H)
		if lw > 110 {
			lw = 110
			lh = lw * float64(logo.H) / float64(logo.W)
		}
		pg.rect(18, 5, lw+8, 48, 1)
		pg.image(22, 5+(48-lh)/2, lw, lh)
		tx = 18 + lw + 8 + 14
	}
	pg.text(tx, 28, 19, stBold, 1, title)
	pg.text(tx, 47, 12, stNormal, 0.85, sexTitle(sex))
	right := strings.TrimSpace(strings.Trim(strings.Join([]string{luogo, date}, "  -  "), " -"))
	if right != "" {
		w := textWidth(right, 10, false)
		pg.text(pg.w-28-w, 47, 10, stNormal, 0.85, right)
	if curFav != nil && strings.TrimSpace(luogo) != "" {
    	pg.rect(pg.w-28-w-36, 23, 32, 32, 1)
	    pg.favicon(pg.w-28-w-34, 25, 30, 30)
		}
	}
	if d == nil {
		pg.text(28, 110, 12, stItalic, 0.3, "Tabellone non ancora sorteggiato.")
		return
	}
	size := d.Size
	R := len(d.Rounds)
	cols := R + 1
	left, right2 := 28.0, pg.w-28
	top, bottom := 96.0, 556.0
	cw := (right2 - left) / float64(cols)
	rh := (bottom - top) / float64(size)
	fs := math.Min(9, math.Max(5.8, rh*0.56))
	smallFs := math.Max(5.2, fs-1.6)

	label := func(id int, first bool) (string, bool) {
		if id == -1 {
			return "BYE", true
		}
		p, ok := pl[id]
		if !ok || id == 0 {
			return "", false
		}
		full, short := p.display()
		rank := strings.TrimSpace(p.Classifica)
		pre := ""
		if s, ok := d.Seeds[id]; ok && first {
			pre = fmt.Sprintf("[%d] ", s)
		}
		suf := ""
		if rank != "" && first {
			suf = " (" + rank + ")"
		}
		maxW := cw - 16
		return fitText([]string{pre + full + suf, pre + short + suf, pre + full, pre + short}, maxW, fs, false), false
	}

	ys := make([][]float64, cols)
	ys[0] = make([]float64, size)
	for k := 0; k < size; k++ {
		ys[0][k] = top + (float64(k)+0.5)*rh
	}
	for c := 1; c < cols; c++ {
		n := len(ys[c-1]) / 2
		ys[c] = make([]float64, n)
		for i := 0; i < n; i++ {
			ys[c][i] = (ys[c-1][2*i] + ys[c-1][2*i+1]) / 2
		}
	}
	colX := func(c int) float64 { return left + float64(c)*cw }
	lineEnd := func(c int) float64 { return colX(c) + cw - 10 }

	// titoli dei turni
	for r := 0; r < R; r++ {
		pg.text(colX(r), 84, 8.5, stBold, 0.25, roundName(size>>uint(r)))
	}
	pg.text(colX(R), 84, 8.5, stBold, 0.25, "Vincitore")

	// colonna 0: iscritti
	for k := 0; k < size; k++ {
		m := d.Rounds[0][k/2]
		id := m.A
		if k%2 == 1 {
			id = m.B
		}
		y := ys[0][k]
		pg.line(colX(0), y, lineEnd(0), y, 0.6, 0.35)
		if s, bye := label(id, true); s != "" {
			if bye {
				pg.text(colX(0)+2, y-2, fs, stItalic, 0.55, s)
			} else {
				pg.text(colX(0)+2, y-2, fs, stNormal, 0, s)
			}
		}
	}
	// connettori e turni successivi
	for r := 0; r < R; r++ {
		for i := range d.Rounds[r] {
			m := d.Rounds[r][i]
			ya, yb := ys[r][2*i], ys[r][2*i+1]
			xe := lineEnd(r)
			pg.line(xe, ya, xe, yb, 0.6, 0.35)
			ym := ys[r+1][i]
			pg.line(xe, ym, colX(r+1), ym, 0.6, 0.35)
			pg.line(colX(r+1), ym, lineEnd(r+1), ym, 0.6, 0.35)
			if m.W > 0 {
				bold := r+1 == R
				fsz := fs
				if bold {
					fsz = math.Min(fs+1.5, 11)
				}
				s, _ := label(m.W, false)
				st := stNormal
				if bold {
					st = stBold
				}
				pg.text(colX(r+1)+2, ym-2, fsz, st, 0, s)
			}
			sc := strings.TrimSpace(m.Score)
			if sc != "" && m.W > 0 {
				pg.text(colX(r+1)+2, ym+smallFs+0.8, smallFs, stNormal, 0.35, sc)
			}
		}
	}
	// piè di pagina
	pg.line(28, 566, pg.w-28, 566, 0.4, 0.7)
	pg.text(28, 579, 7, stNormal, 0.4, "[n] = testa di serie    (x.y) = classifica FITP    BYE = passa il turno    Generato il "+stamp)
}

func buildPDF(which string) ([]byte, error) {
	doc := &pdfDoc{}
	if im, err := loadLogo(logoBytes()); err == nil {
		doc.logo = im
	} else if im, err := loadLogo(logoDefault); err == nil {
		doc.logo = im
	}
	curFav = nil
	if fi, err := loadLogo(faviconBytes()); err == nil {
		doc.fav = fi
		curFav = fi
	}
	pl := map[int]*Player{}
	for i := range st.Players {
		pl[st.Players[i].ID] = &st.Players[i]
	}
	for i := range st.Coppie {
		pl[st.Coppie[i].ID] = &st.Coppie[i]
	}
	stamp := nowStamp()
	for _, sex := range catKeys {
		if which != "all" && which != sex {
			continue
		}
		if rr := st.RRs[sex]; rr != nil {
			stand := rrStandings(rr)
			for gi := range rr.Groups {
				drawRRGroup(doc.newPage(), sex, rr, gi, stand[gi], pl, st.Title, st.Date, st.Luogo, stamp, doc.logo)
			}
			continue
		}
		d := st.Draws[sex]
		if d == nil && which == "all" {
			continue
		}
		pg := doc.newPage()
		drawBracket(pg, sex, d, pl, st.Title, st.Date, st.Luogo, stamp, doc.logo)
	}
	if len(doc.pages) == 0 {
		return nil, fmt.Errorf("nessun tabellone da esportare: fai prima il sorteggio")
	}
	return doc.bytes(), nil
}

// drawRRGroup disegna una pagina per ogni girone: classifica a sinistra, partite a destra.
func drawRRGroup(pg *page, key string, rr *RR, gi int, stand []Stand, pl map[int]*Player, title, date, luogo, stamp string, logo *pdfImg) {
	pg.rect(0, 0, pg.w, 58, 0.14)
	tx := 28.0
	if logo != nil {
		lh := 44.0
		lw := lh * float64(logo.W) / float64(logo.H)
		if lw > 110 {
			lw = 110
			lh = lw * float64(logo.H) / float64(logo.W)
		}
		pg.rect(18, 5, lw+8, 48, 1)
		pg.image(22, 5+(48-lh)/2, lw, lh)
		tx = 18 + lw + 8 + 14
	}
	pg.text(tx, 28, 19, stBold, 1, title)
	pg.text(tx, 47, 12, stNormal, 0.85, fmt.Sprintf("%s - girone %c", catTitle(key), 'A'+gi))
	right := strings.TrimSpace(strings.Trim(strings.Join([]string{luogo, date}, "  -  "), " -"))
	if right != "" {
		w := textWidth(right, 10, false)
		pg.text(pg.w-28-w, 47, 10, stNormal, 0.85, right)
		if curFav != nil && strings.TrimSpace(luogo) != "" {
    		pg.rect(pg.w-28-w-36, 23, 32, 32, 1)
    		pg.favicon(pg.w-28-w-34, 25, 30, 30)
		}
	}
	name := func(id int, maxW, size float64) string {
		p := pl[id]
		if p == nil {
			return "?"
		}
		full, short := p.display()
		return fitText([]string{full, short}, maxW, size, false)
	}
	// classifica del girone
	pg.text(28, 90, 9, stBold, 0.25, "Classifica")
	hx := []float64{28, 48, 250, 276, 302, 332, 378}
	for i, h := range []string{"#", "Giocatore", "V", "P", "Set", "Game", "Pt"} {
		pg.text(hx[i], 108, 8, stBold, 0.4, h)
	}
	pg.line(28, 112, 430, 112, 0.5, 0.5)
	y := 128.0
	for _, s := range stand {
		pg.text(hx[0], y, 10, stNormal, 0, strconv.Itoa(s.Pos))
		pg.text(hx[1], y, 10, stNormal, 0, name(s.ID, 195, 10))
		pg.text(hx[2], y, 10, stNormal, 0, strconv.Itoa(s.V))
		pg.text(hx[3], y, 10, stNormal, 0, strconv.Itoa(s.P))
		pg.text(hx[4], y, 10, stNormal, 0, fmt.Sprintf("%d-%d", s.SV, s.SP))
		pg.text(hx[5], y, 10, stNormal, 0, fmt.Sprintf("%d-%d", s.GV, s.GP))
		pg.text(hx[6], y, 10, stBold, 0, strconv.Itoa(s.Pt))
		pg.line(28, y+5, 430, y+5, 0.3, 0.8)
		y += 20
	}
	// partite
	pg.text(470, 90, 9, stBold, 0.25, "Partite")
	pg.line(470, 112, 814, 112, 0.5, 0.5)
	my := 128.0
	for _, m := range rr.Matches {
		if m.G != gi {
			continue
		}
		if my > 548 {
			break
		}
		pg.text(470, my, 8, stNormal, 0.4, fmt.Sprintf("T%d", m.R))
		a, b := name(m.A, 120, 9), name(m.B, 120, 9)
		stA, stB := stNormal, stNormal
		if m.W == m.A {
			stA = stBold
		} else if m.W == m.B {
			stB = stBold
		}
		pg.text(492, my, 9, stA, 0, a)
		pg.text(492+125, my, 9, stNormal, 0.5, "-")
		pg.text(492+135, my, 9, stB, 0, b)
		if m.W > 0 && strings.TrimSpace(m.Score) != "" {
			pg.text(492+262, my, 8.5, stNormal, 0.25, m.Score)
		}
		my += 16
	}
	pg.line(28, 566, pg.w-28, 566, 0.4, 0.7)
	pg.text(28, 579, 7, stNormal, 0.4, "Vittoria 2 punti, sconfitta 1, W.O. 0. A pari punti: scontro diretto, poi differenza set e game.    Formato: "+fmtLabel(rr.Fmt)+"    Generato il "+stamp)
}

func fmtLabel(f string) string {
	if f == "stb" {
		return "2 set + tie-break a 10"
	}
	return "al meglio dei 3 set"
}
