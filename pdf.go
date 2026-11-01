package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/png"
	"math"
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

func textWidth(s string, size float64, bold bool) float64 {
	w := 0
	for _, r := range s {
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
	for i, p := range d.pages {
		res := "/Font << /F1 3 0 R /F2 4 0 R /F3 5 0 R >>"
		if d.logo != nil {
			res += fmt.Sprintf(" /XObject << /Im1 %d 0 R >>", 6+2*len(d.pages))
		}
		obj(fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << %s >> /Contents %d 0 R >>\nendobj\n", 6+2*i, p.w, p.h, res, 7+2*i))
		obj(fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", 7+2*i, p.b.Len(), p.b.String()))
	}
	if im := d.logo; im != nil {
		id := 6 + 2*len(d.pages)
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
	if sex == "F" {
		return "Singolare femminile"
	}
	return "Singolare maschile"
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
	pl := map[int]*Player{}
	for i := range st.Players {
		pl[st.Players[i].ID] = &st.Players[i]
	}
	stamp := nowStamp()
	for _, sex := range []string{"M", "F"} {
		if which != "all" && which != sex {
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
