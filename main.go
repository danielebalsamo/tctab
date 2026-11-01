package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
)

type Giocatore struct {
	Nome      string `json:"nome"`
	Cognome   string `json:"cognome"`
	Tessera   string `json:"tessera"`
	Classifica string `json:"classifica"`
	Circolo   string `json:"circolo"`
}

type AppState struct {
	mu        sync.RWMutex
	Giocatori []Giocatore `json:"giocatori"`
}

var state = &AppState{
	Giocatori: make([]Giocatore, 0),
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.Handle("/", http.FileServer(http.Dir("./")))
	http.HandleFunc("/api/import-tenniscall", handleTenniscallImport)
	http.HandleFunc("/api/giocatori", handleGetGiocatori)

	log.Printf("Server avviato sulla porta %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Errore avvio server: %v", err)
	}
}

func handleTenniscallImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	// 1. Download CSV Tenniscall
	tenniscallURL := "https://admin.tenniscall.com/it/tennis-center/import-customers?id=6435"
	reqTC, _ := http.NewRequest("GET", tenniscallURL, nil)
	reqTC.Header.Set("User-Agent", "Mozilla/5.0")
	respTC, err := http.DefaultClient.Do(reqTC)
	if err != nil || respTC.StatusCode != http.StatusOK {
		http.Error(w, "Errore download da Tenniscall", http.StatusBadGateway)
		return
	}
	defer respTC.Body.Close()
	tcData, _ := io.ReadAll(respTC.Body)

	// 2. Parsing CSV
	rCSV := csv.NewReader(bytes.NewReader(tcData))
	rCSV.LazyQuotes = true
	rCSV.FieldsPerRecord = -1
	records, err := rCSV.ReadAll()
	if err != nil {
		http.Error(w, "Errore parsing CSV: "+err.Error(), http.StatusBadRequest)
		return
	}

	var nuoviGiocatori []Giocatore
	for i, record := range records {
		if i == 0 || len(record) < 4 {
			continue // Salva intestazione o righe malformate
		}
		// Adatta gli indici in base alla struttura effettiva delle colonna del tuo CSV Tenniscall
		g := Giocatore{
			Nome:       strings.TrimSpace(record[1]),
			Cognome:    strings.TrimSpace(record[0]),
			Tessera:    strings.TrimSpace(record[2]),
			Classifica: strings.TrimSpace(record[3]),
			Circolo:    "",
		}
		nuoviGiocatori = append(nuoviGiocatori, g)
	}

	state.mu.Lock()
	state.Giocatori = nuoviGiocatori
	state.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Importazione riuscita! Caricati %d giocatori.", len(nuoviGiocatori)),
		"count":   len(nuoviGiocatori),
	})
}

func handleGetGiocatori(w http.ResponseWriter, r *http.Request) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state.Giocatori)
}