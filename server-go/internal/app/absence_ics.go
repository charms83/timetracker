package app

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

/* Abwesenheiten als Kalender-Abo (wie backend/server.js GET /api/calendar/abwesenheiten.ics).
   Browser-Download: Sitzungs-Cookie. Abo in Calendar.app: Geräte-Token als ?token=zpdev_… */

var absenceICSLabels = map[string]string{
	"urlaub": "Urlaub", "krankheit": "Krank", "feiertag": "Feiertag",
	"fortbildung": "Fortbildung", "sonstiges": "Abwesend", "gleitzeit": "Gleitzeit",
}

func icsEsc(t string) string {
	s := strings.ReplaceAll(t, `\`, `\\`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, "\r\n", `\n`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func icsDate8(d string) string { return strings.ReplaceAll(d, "-", "") }

func icsNextDay(d string) string {
	t, err := time.ParseInLocation("2006-01-02", d, time.UTC)
	if err != nil {
		return d
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

func buildAbsencesICS(rows []map[string]any, now time.Time) string {
	stamp := now.UTC().Format("20060102T150405Z")
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Zeitprotokoll//Team-Server//DE\r\nCALSCALE:GREGORIAN\r\nMETHOD:PUBLISH\r\n")
	b.WriteString("X-WR-CALNAME:Zeitprotokoll Abwesenheiten\r\nX-WR-TIMEZONE:Europe/Berlin\r\n")
	b.WriteString("REFRESH-INTERVAL;VALUE=DURATION:PT1H\r\nX-PUBLISHED-TTL:PT1H\r\n")
	for _, a := range rows {
		typ := asStr(a["type"])
		lbl := absenceICSLabels[typ]
		if lbl == "" {
			lbl = typ
		}
		note := asStr(a["note"])
		sum := lbl
		if note != "" {
			sum += " – " + note
		}
		from, to := asStr(a["date_from"]), asStr(a["date_to"])
		if to == "" {
			to = from
		}
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:zp-abs-%v@zeitprotokoll\r\nDTSTAMP:%s\r\n", a["id"], stamp)
		fmt.Fprintf(&b, "DTSTART;VALUE=DATE:%s\r\nDTEND;VALUE=DATE:%s\r\n", icsDate8(from), icsDate8(icsNextDay(to)))
		fmt.Fprintf(&b, "SUMMARY:%s\r\nTRANSP:TRANSPARENT\r\nCATEGORIES:%s\r\n", icsEsc(sum), icsEsc(lbl))
		if h := asStr(a["hours"]); h != "" {
			fmt.Fprintf(&b, "DESCRIPTION:%s\r\n", icsEsc(h+" h"))
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func (a *App) routesAbsenceICS(mux *http.ServeMux) {
	h := a.absencesICS
	mux.HandleFunc("GET /api/calendar/abwesenheiten.ics", h)
	mux.HandleFunc("GET /api/absences.ics", h)
}

// absencesICSAuth: Sitzung oder Geräte-Token (Header / Bearer / ?token= für Kalender-Abo)
func (a *App) absencesICSAuth(r *http.Request) (User, bool) {
	if u, ok := a.sessionUser(r); ok && u.timeAccount() {
		return u, true
	}
	raw := strings.TrimSpace(r.URL.Query().Get("token"))
	if raw == "" {
		raw = r.Header.Get("X-ZP-Device-Token")
	}
	if raw == "" {
		if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
			raw = strings.TrimSpace(strings.TrimPrefix(ah, "Bearer "))
		}
	}
	if raw == "" || !strings.HasPrefix(raw, deviceTokenPrefix) || len(raw) > 200 {
		return User{}, false
	}
	// Query-Token nur für diesen Feed – gleiche Hash-Logik wie deviceUser
	ctx := r.Context()
	u, did, ok := a.lookupDeviceUser(ctx, a.deviceTokenHash(raw))
	if !ok {
		u, did, ok = a.lookupDeviceUser(ctx, deviceTokenHashLegacy(raw))
		if !ok {
			return User{}, false
		}
		_, _ = a.DB.Exec(ctx, `UPDATE device_tokens SET token_hash=$1 WHERE id=$2 AND revoked_at IS NULL`, a.deviceTokenHash(raw), did)
	}
	if !u.timeAccount() {
		return User{}, false
	}
	_, _ = a.DB.Exec(ctx, `UPDATE device_tokens SET last_used=now() WHERE id=$1 AND (last_used IS NULL OR last_used < now() - interval '1 minute')`, did)
	return u, true
}

func (a *App) absencesICS(w http.ResponseWriter, r *http.Request) {
	u, ok := a.absencesICSAuth(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "login_required"})
		return
	}
	rows, err := a.maps(r.Context(), `SELECT id, date_from, date_to, type, hours, note FROM absences WHERE user_id=$1 ORDER BY date_from, id`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	body := buildAbsencesICS(rows, time.Now())
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=300")
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="zeitprotokoll-abwesenheiten.ics"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}
