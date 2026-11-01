package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type xcell struct {
	R  string `xml:"r,attr"`
	T  string `xml:"t,attr"`
	V  string `xml:"v"`
	IS struct {
		T []string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}

func colIndex(ref string) int {
	n := 0
	for _, c := range ref {
		if c >= 'A' && c <= 'Z' {
			n = n*26 + int(c-'A'+1)
		} else if c >= 'a' && c <= 'z' {
			n = n*26 + int(c-'a'+1)
		} else {
			break
		}
	}
	return n - 1
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// readXLSX legge il primo foglio di un file .xlsx (stringhe condivise o inline).
func readXLSX(data []byte) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("il file non e' un Excel (.xlsx) valido")
	}
	files := map[string]*zip.File{}
	var sheets []string
	for _, f := range zr.File {
		files[f.Name] = f
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheets = append(sheets, f.Name)
		}
	}
	if len(sheets) == 0 {
		return nil, errors.New("nessun foglio trovato nel file Excel")
	}
	sort.Strings(sheets)
	var shared []string
	if f, ok := files["xl/sharedStrings.xml"]; ok {
		b, err := readZipFile(f)
		if err == nil {
			var s struct {
				SI []struct {
					T []string `xml:"t"`
					R []struct {
						T string `xml:"t"`
					} `xml:"r"`
				} `xml:"si"`
			}
			if xml.Unmarshal(b, &s) == nil {
				for _, si := range s.SI {
					var sb strings.Builder
					for _, t := range si.T {
						sb.WriteString(t)
					}
					for _, r := range si.R {
						sb.WriteString(r.T)
					}
					shared = append(shared, sb.String())
				}
			}
		}
	}
	b, err := readZipFile(files[sheets[0]])
	if err != nil {
		return nil, err
	}
	var sh struct {
		Rows []struct {
			C []xcell `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(b, &sh); err != nil {
		return nil, err
	}
	var out [][]string
	for _, r := range sh.Rows {
		var row []string
		for ci, c := range r.C {
			idx := colIndex(c.R)
			if idx < 0 {
				idx = ci
			}
			for len(row) <= idx {
				row = append(row, "")
			}
			val := c.V
			switch c.T {
			case "s":
				if n, err := strconv.Atoi(strings.TrimSpace(c.V)); err == nil && n >= 0 && n < len(shared) {
					val = shared[n]
				}
			case "inlineStr":
				var sb strings.Builder
				for _, t := range c.IS.T {
					sb.WriteString(t)
				}
				for _, r := range c.IS.R {
					sb.WriteString(r.T)
				}
				val = sb.String()
			}
			row[idx] = strings.TrimSpace(val)
		}
		out = append(out, row)
	}
	return out, nil
}

func fixMojibake(s string) string {
	if !strings.ContainsAny(s, "ÃÂ") {
		return s
	}
	var b []byte
	for _, r := range s {
		if r > 255 {
			return s
		}
		b = append(b, byte(r))
	}
	if utf8.Valid(b) {
		return string(b)
	}
	return s
}

// decodeText legge testo UTF-8, UTF-16 (con o senza BOM) o Latin-1, e corregge la doppia codifica.
func decodeText(b []byte) string {
	var out string
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		out = string(utf16.Decode(u))
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i+1])|uint16(b[i])<<8)
		}
		out = string(utf16.Decode(u))
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		out = string(b[3:])
	default:
		if bytes.Count(b, []byte{0}) > len(b)/4 {
			b = bytes.ReplaceAll(b, []byte{0}, nil)
		}
		if utf8.Valid(b) {
			out = string(b)
		} else {
			r := make([]rune, len(b))
			for i, c := range b {
				r[i] = rune(c)
			}
			out = string(r)
		}
	}
	return fixMojibake(out)
}

func readCSVRows(data []byte) ([][]string, error) {
	text := decodeText(data)
	first := text
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	cr := csv.NewReader(strings.NewReader(text))
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	if strings.Count(first, ";") >= strings.Count(first, ",") {
		cr.Comma = ';'
	}
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		for i := range r {
			r[i] = strings.TrimSpace(r[i])
		}
	}
	return recs, nil
}

func readTable(data []byte) ([][]string, error) {
	if len(data) > 2 && data[0] == 'P' && data[1] == 'K' {
		return readXLSX(data)
	}
	return readCSVRows(data)
}
