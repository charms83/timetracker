package app

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

/* Feiertage Niedersachsen 2024–2027 (aus backend/server.js, NI_HOLIDAYS). */

type holiday struct{ date, name string }

var niHolidays = []holiday{
	{"2024-01-01", "Neujahr"},
	{"2024-03-29", "Karfreitag"},
	{"2024-04-01", "Ostermontag"},
	{"2024-05-01", "Tag der Arbeit"},
	{"2024-05-09", "Christi Himmelfahrt"},
	{"2024-05-20", "Pfingstmontag"},
	{"2024-10-03", "Tag der Deutschen Einheit"},
	{"2024-10-31", "Reformationstag"},
	{"2024-12-25", "1. Weihnachtstag"},
	{"2024-12-26", "2. Weihnachtstag"},
	{"2025-01-01", "Neujahr"},
	{"2025-04-18", "Karfreitag"},
	{"2025-04-21", "Ostermontag"},
	{"2025-05-01", "Tag der Arbeit"},
	{"2025-05-29", "Christi Himmelfahrt"},
	{"2025-06-09", "Pfingstmontag"},
	{"2025-10-03", "Tag der Deutschen Einheit"},
	{"2025-10-31", "Reformationstag"},
	{"2025-12-25", "1. Weihnachtstag"},
	{"2025-12-26", "2. Weihnachtstag"},
	{"2026-01-01", "Neujahr"},
	{"2026-04-03", "Karfreitag"},
	{"2026-04-06", "Ostermontag"},
	{"2026-05-01", "Tag der Arbeit"},
	{"2026-05-14", "Christi Himmelfahrt"},
	{"2026-05-25", "Pfingstmontag"},
	{"2026-10-03", "Tag der Deutschen Einheit"},
	{"2026-10-31", "Reformationstag"},
	{"2026-12-25", "1. Weihnachtstag"},
	{"2026-12-26", "2. Weihnachtstag"},
	{"2027-01-01", "Neujahr"},
	{"2027-03-26", "Karfreitag"},
	{"2027-03-29", "Ostermontag"},
	{"2027-05-01", "Tag der Arbeit"},
	{"2027-05-06", "Christi Himmelfahrt"},
	{"2027-05-17", "Pfingstmontag"},
	{"2027-10-03", "Tag der Deutschen Einheit"},
	{"2027-10-31", "Reformationstag"},
	{"2027-12-25", "1. Weihnachtstag"},
	{"2027-12-26", "2. Weihnachtstag"},
}

func (a *App) routesHolidays(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/absences/import-holidays", a.auth(a.holidaysImport))
}

// holidaysImport legt die Feiertage als Abwesenheit (type feiertag) an; vorhandene Tage werden übersprungen.
func (a *App) holidaysImport(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	year := strings.TrimSpace(asStr(in["year"]))
	if year != "" {
		if _, err := strconv.Atoi(year); err != nil {
			badRequest(w, "year ungültig")
			return
		}
	}
	imported, skipped := 0, 0
	err := a.inTx(r.Context(), func(tx pgx.Tx) error {
		for _, h := range niHolidays {
			if year != "" && !strings.HasPrefix(h.date, year) {
				continue
			}
			var exists bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM absences WHERE user_id=$1 AND date_from=$2 AND type='feiertag')`, u.ID, h.date).Scan(&exists); err != nil {
				return err
			}
			if exists {
				skipped++
				continue
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO absences(user_id,date_from,date_to,type,hours,note) VALUES($1,$2,$2,'feiertag',NULL,$3)`, u.ID, h.date, h.name); err != nil {
				return err
			}
			imported++
		}
		return nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped})
}
