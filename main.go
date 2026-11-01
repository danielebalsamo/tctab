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
	"strings"
	"sync"
)

// AppState mantiene lo stato dei giocatori e delle configurazioni in memoria
type AppState struct {
	mu        sync.RWMutex
	Giocatori []Giocatore `json:"giocatori"`
	FitpMap   map[string]FitpInfo
}

var state = &AppState{
	FitpMap: make(map[string]FitpInfo),
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// File statici (Frontend)
	http.Handle("/", http.FileServer(http.Dir("./")))

	// Endpoints API attivi
	http.HandleFunc("/api/import-tenniscall", handleTenniscallImport)
	http.HandleFunc("/api/giocatori", handleGetGiocatori)

	log.Printf("Server avviato sulla porta %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Errore durante l'avvio del server: %v", err)
	}
}

// handleTenniscallImport gestisce l'importazione diretta via HTTP senza passare dal client locale
func handleTenniscallImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	// 1. Download del file clienti da Tenniscall
	tenniscallURL := "https://admin.tenniscall.com/it/tennis-center/import-customers?id=6435"
	reqTC, err := http.NewRequest("GET", tenniscallURL, nil)
	if err != nil {
		http.Error(w, "Errore nella creazione della richiesta Tenniscall: "+err.Error(), http.StatusInternalServerError)
		return
	}
	reqTC.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	respTC, err := http.DefaultClient.Do(reqTC)
	if err != nil || respTC.StatusCode != http.StatusOK {
		msg := "Errore nel download dei dati da Tenniscall"
		if err != nil {
			msg += ": " + err.Error()
		}
		http.Error(w, msg, http.StatusBadGateway)
		return
	}
	defer respTC.Body.Close()

	tcData, err := io.ReadAll(respTC.Body)
	if err != nil {
		http.Error(w, "Errore nella lettura dei dati Tenniscall: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 2. Download dello script FITP
	fitpURL := "https://www.fitp.it/common/vue/giocatoreSearch_V2.3.js"
	respFITP, err := http.Get(fitpURL)
	if err != nil || respFITP.StatusCode != http.StatusOK {
		log.Println("Avviso: Impossibile scaricare aggiornamenti FITP, continuo con la mappa corrente.")
	} else {
		defer respFITP.Body.Close()
		fitpBody, _ := io.ReadAll(respFITP.Body)
		// Aggiorna la mappa dei tesserati FITP in memoria
		nuovaMappa := ParseFITPScript(string(fitpBody))
		state.mu.Lock()
		state.FitpMap = nuovaMappa
		state.mu.Unlock()
	}

	// 3. Parsing del CSV Tenniscall direttamente in RAM
	rCSV := csv.NewReader(bytes.NewReader(tcData))
	rCSV.LazyQuotes = true
	rCSV.FieldsPerRecord = -1

	records, err := rCSV.ReadAll()
	if err != nil {
		http.Error(w, "Errore nel parsing del CSV Tenniscall: "+err.Error(), http.StatusBadRequest)
		return
	}

	state.mu.RLock()
	fitpMapCurrent := state.FitpMap
	state.mu.RUnlock()

	nuoviGiocatori := make([]Giocatore, 0)
	for i, record := range records {
		if i == 0 {
			continue // Salva la riga di intestazione
		}
		g := ElaboraRigaGiocatore(record, fitpMapCurrent)
		nuoviGiocatori = append(nuoviGiocatori, g)
	}

	// 4. Aggiornamento dello stato globale del server
	state.mu.Lock()
	state.Giocatori = nuoviGiocatori
	state.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Importazione completata con successo! Caricati %d giocatori.", len(nuoviGiocatori)),
		"count":   len(nuoviGiocatori),
	})
}

func handleGetGiocatori(w http.ResponseWriter, r *http.Request) {
	state.mu.RLock()
	defer state.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state.Giocatori)
}