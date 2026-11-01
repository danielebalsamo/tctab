TABELLONE TC SALEMI SU RENDER (render.com)
==========================================

ATTENZIONE AL PIANO
Il piano GRATUITO di Render si spegne dopo 15 minuti senza visite e CANCELLA i file a ogni riavvio:
perderesti iscritti, tabelloni e risultati. Per questo programma serve un piano a pagamento con
DISCO PERMANENTE (e' gia' configurato in render.yaml: piano "starter" + disco da 1 GB).
Controlla i prezzi aggiornati su https://render.com/pricing

PASSI
1) Crea un repository PRIVATO su GitHub (o GitLab) e carica tutti i file di questa cartella.
   Il file .gitignore evita di caricare i dati. NON caricare mai classifiche_fitp.xlsx o i file .json:
   contengono i nomi dei soci.
2) Su Render: New + -> Blueprint -> collega il repository -> Render legge render.yaml.
3) Quando te lo chiede, scrivi la password in TORNEO_PASSWORD (sceglila lunga). L'utente e' tcsalemi.
4) Premi Apply e aspetta che il deploy sia "Live" (qualche minuto).
5) Apri l'indirizzo https://tabellone-tc-salemi.onrender.com (o quello che ti mostra Render):
   Render fornisce gia' HTTPS. Accedi con utente e password.
6) Carica l'elenco dei giocatori dal browser: scheda Iscritti -> "Carica classifiche da Excel...".
   Il logo e' gia' incorporato nel programma.

DOPO
- I dati stanno sul disco di Render (cartella /data). Dalla Dashboard del servizio -> Shell
  puoi copiare i file torneo_tc_salemi.json e classifica_db.json per farne un backup.
- Per aggiornare il programma: carica i nuovi file su GitHub, Render ripubblica da solo.
- Per cambiare la password: Dashboard -> Environment -> TORNEO_PASSWORD (il servizio riparte).
- Se piu' persone modificano insieme, vale l'ultimo salvataggio.
- "Aggiorna dal portale FITP" partira' dai server di Render: usa "Prova connessione al portale"
  nella scheda Classifica per vedere subito se il portale risponde.
