package main

import (
	"bytes"
	"encoding/csv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

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
