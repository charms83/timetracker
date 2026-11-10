package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

/* Nachtragen nach Monatsschluss (Regel 2): In einem geschlossenen, eingereichten oder freigegebenen Monat schreibt die Person selbst
   nichts mehr (409 month_locked). Nachtragen dürfen nur die Teamleitung der Person (Leitung im selben Team) und das Personal (is_hr) –
   nie der technische Administrator über seine Rolle, nie der Notfallzugang, nie für den eigenen Monat.
   Eigene Routen /api/team/{userId}/…, Pflichtfeld „reason“ (10–500 Zeichen), gleiche Prüfungen wie die normalen Schreibwege
   (entries.go/absences.go). Die Datenbank-Sperre wird nur in der Schreib-Transaktion und nur für die Zielperson aufgehoben
   (set_config zp.bypass_user/zp.actor, transaktionslokal; Trigger aus Migration 018). Protokoll: entry_audit (actor, origin 'nachtrag'),
   audit_log 'nachtrag', month_approval_log 'nachtrag'; Hinweis an die Person (approvalNotices). */

var (
	errMonthOpen = errors.New("Monat ist offen – bitte normal eintragen")
	errBadDate   = errors.New("Datum ungültig")
)

// entryWriter: wer schreibt für wen. Normal: Ziel = Akteur. Nachtrag: Akteur schreibt für eine andere Person (nur nach Prüfung).
type entryWriter struct {
	uid     int64  // Eigentümer der Daten (Ziel)
	actor   int64  // wer schreibt
	reason  string // Begründung (nur Nachtrag)
	checked bool   // Berechtigung geprüft (nur nachtragWriter setzt das)
}

func selfWriter(u User) entryWriter { return entryWriter{uid: u.ID, actor: u.ID} }

func (wr entryWriter) nachtrag() bool { return wr.actor != wr.uid }

// enteredBy: Wert für time_entries.entered_by (NULL = die Person selbst)
func (wr entryWriter) enteredBy() any {
	if wr.nachtrag() {
		return wr.actor
	}
	return nil
}

// writeTx: Schreib-Transaktion; beim Nachtrag mit kontrollierter Umgehung der Monatssperre für genau die Zielperson
func (a *App) writeTx(ctx context.Context, wr entryWriter, fn func(tx pgx.Tx) error) error {
	if wr.nachtrag() && (!wr.checked || wr.uid <= 0 || wr.actor <= 0) {
		return errors.New("Nachtrag ohne Berechtigungsprüfung")
	}
	return a.inTx(ctx, func(tx pgx.Tx) error {
		if wr.nachtrag() {
			if _, err := tx.Exec(ctx, `SELECT set_config('zp.bypass_user', $1, true), set_config('zp.actor', $2, true)`,
				strconv.FormatInt(wr.uid, 10), strconv.FormatInt(wr.actor, 10)); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

// requireLocked: beim Nachtrag müssen alle berührten Tage in gesperrten Monaten liegen (sonst gilt der normale Weg der Person).
// zp_month_locked nimmt die gemeinsame Sperre der Person: Status kann sich bis zum Ende der Transaktion nicht ändern.
func (a *App) requireLocked(ctx context.Context, tx pgx.Tx, wr entryWriter, dates ...string) error {
	if !wr.nachtrag() {
		return nil
	}
	for _, m := range distinctMonths(dates) {
		locked, err := monthLockedTx(ctx, tx, wr.uid, m+"-01")
		if err != nil {
			return err
		}
		if !locked {
			return errMonthOpen
		}
	}
	return nil
}

// distinctMonths: Monate (JJJJ-MM) der angegebenen Tage, sortiert; leere Werte zählen nicht, ungültige als "" (→ Fehler beim Aufrufer)
func distinctMonths(dates []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range dates {
		if d == "" {
			continue
		}
		m := ""
		if isoDateRE.MatchString(d) {
			m = d[:7]
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// monthsOfRange: alle Monate von from bis to (JJJJ-MM-TT), höchstens 30
func monthsOfRange(from, to string) []string {
	f, ok1 := parseDay(from)
	t, ok2 := parseDay(to)
	if !ok1 || !ok2 || t.Before(f) {
		return []string{""}
	}
	var out []string
	for m := f.AddDate(0, 0, 1-f.Day()); !m.After(t) && len(out) < 30; m = m.AddDate(0, 1, 0) {
		out = append(out, dayStr(m))
	}
	return out
}

// nachtragLog: Verlauf (month_approval_log) und Audit (audit_log) in derselben Transaktion wie die Änderung
func (a *App) nachtragLog(ctx context.Context, tx pgx.Tx, wr entryWriter, what, ref string, dates ...string) error {
	if !wr.nachtrag() {
		return nil
	}
	var ds []string
	for _, d := range dates {
		if d != "" && !contains(ds, d) {
			ds = append(ds, d)
		}
	}
	months := distinctMonths(dates)
	for _, m := range months {
		if m == "" {
			return errBadDate
		}
		if _, err := tx.Exec(ctx, `INSERT INTO month_approval_log(user_id,month,action,actor_id,comment) VALUES($1,$2,'nachtrag',$3,$4)`,
			wr.uid, m, wr.actor, what+" "+ref+" ("+strings.Join(ds, ", ")+"): "+wr.reason); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_log(user_id,action,detail) VALUES($1,'nachtrag',$2)`, wr.actor,
		fmt.Sprintf("Person %d: %s %s, Datum %s, Grund: %s", wr.uid, what, ref, strings.Join(ds, ", "), wr.reason))
	return err
}

// writeFail: Fehler eines Schreibwegs beantworten (fachliche Ablehnungen statt 500)
func (a *App) writeFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMonthOpen):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": errMonthOpen.Error(), "code": "month_open"})
	case errors.Is(err, errBadDate):
		badRequest(w, errBadDate.Error())
	case errors.Is(err, pgx.ErrNoRows):
		notFound(w)
	default:
		a.fail(w, err)
	}
}

// afterWrite: Abgleich anstoßen; beim Nachtrag zusätzlich die Benachrichtigungs-Stelle je Monat
func (a *App) afterWrite(ctx context.Context, wr entryWriter, dates ...string) {
	a.syncAuto(wr.uid) // nachgetragene Zeiten gehen wie normale nach Plane (Abgleichsfelder sind nicht gesperrt)
	if !wr.nachtrag() {
		return
	}
	for _, m := range distinctMonths(dates) {
		if m != "" {
			a.notifyApproval(ctx, evNachtrag, wr.uid, m)
		}
	}
}

/* ── Berechtigung ────────────────────────────────────────────────── */

// nachtragAllowed: reine Regel. Personal oder Teamleitung der Zielperson; nie man selbst, nie Notfallzugang, nie ein Konto ohne Zeiterfassung.
// Die Rolle „admin“ zählt nicht – ein Administrator darf nur, wenn er zugleich Personal oder Teamleitung der Person ist.
func nachtragAllowed(actorID, target int64, actorRole string, actorTime, isHR, leadOfTarget, targetOK bool) bool {
	if actorID <= 0 || target <= 0 || actorID == target || actorRole == "breakglass" || !actorTime || !targetOK {
		return false
	}
	return isHR || leadOfTarget
}

// nachtragWriter prüft Akteur, Zielperson und Begründung. ok=false: Antwort ist schon gesendet.
func (a *App) nachtragWriter(w http.ResponseWriter, r *http.Request, u User, in body) (entryWriter, bool) {
	ctx := r.Context()
	target, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil || target <= 0 {
		badRequest(w, "userId ungültig")
		return entryWriter{}, false
	}
	var targetOK bool
	if err := a.DB.QueryRow(ctx, `SELECT NOT (kind='local' AND NOT productive) FROM users WHERE id=$1 AND role<>'breakglass'`, target).Scan(&targetOK); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		a.fail(w, err)
		return entryWriter{}, false
	}
	lead := false
	if u.IsLead && u.ID != target && u.Role != "breakglass" {
		lead = a.canSee(ctx, User{ID: u.ID, IsLead: true}, target) // nur die Team-Beziehung (ohne Personal-Abkürzung)
	}
	if !nachtragAllowed(u.ID, target, u.Role, u.timeAccount(), u.IsHR, lead, targetOK) {
		a.audit(ctx, u.ID, "nachtrag.denied", fmt.Sprintf("%s %s", r.Method, r.URL.Path))
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
		return entryWriter{}, false
	}
	reason, msg := nachtragReason(in["reason"])
	if msg != "" {
		badRequest(w, msg)
		return entryWriter{}, false
	}
	delete(in, "reason")
	return entryWriter{uid: target, actor: u.ID, reason: reason, checked: true}, true
}

// nachtragReason: Begründung 10–500 Zeichen, ohne Steuerzeichen (Zeilenumbruch erlaubt)
func nachtragReason(v any) (string, string) {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	n := len([]rune(s))
	if n < 10 || n > 500 {
		return "", "Begründung (reason): 10 bis 500 Zeichen"
	}
	for _, c := range s {
		if (c < 0x20 && c != '\n' && c != '\t') || c == 0x7f {
			return "", "Begründung enthält Steuerzeichen"
		}
	}
	return s, ""
}

/* ── Routen ──────────────────────────────────────────────────────── */

func (a *App) routesNachtrag(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/team/{userId}/time-entries", a.auth(a.nachtragEntryCreate))
	mux.HandleFunc("PUT /api/team/{userId}/time-entries/{id}", a.auth(a.nachtragEntryUpdate))
	mux.HandleFunc("DELETE /api/team/{userId}/time-entries/{id}", a.auth(a.nachtragEntryDelete))
	mux.HandleFunc("POST /api/team/{userId}/absences", a.auth(a.nachtragAbsenceCreate))
	mux.HandleFunc("PUT /api/team/{userId}/absences/{id}", a.auth(a.nachtragAbsenceUpdate))
	mux.HandleFunc("DELETE /api/team/{userId}/absences/{id}", a.auth(a.nachtragAbsenceDelete))
}

// nachtragPrep: Eingabe lesen, Pfad-Id (falls nötig) und Berechtigung prüfen
func (a *App) nachtragPrep(w http.ResponseWriter, r *http.Request, u User, needID bool) (entryWriter, int64, body, bool) {
	var id int64
	if needID {
		var ok bool
		if id, ok = pathID(r); !ok || id <= 0 {
			notFound(w)
			return entryWriter{}, 0, nil, false
		}
	}
	var in body
	if !readJSON(w, r, &in) {
		return entryWriter{}, 0, nil, false
	}
	if in == nil {
		in = body{}
	}
	wr, ok := a.nachtragWriter(w, r, u, in)
	return wr, id, in, ok
}

func (a *App) nachtragEntryCreate(w http.ResponseWriter, r *http.Request, u User) {
	if wr, _, in, ok := a.nachtragPrep(w, r, u, false); ok {
		a.entryCreate(w, r, wr, in)
	}
}

func (a *App) nachtragEntryUpdate(w http.ResponseWriter, r *http.Request, u User) {
	if wr, id, in, ok := a.nachtragPrep(w, r, u, true); ok {
		a.entryUpdate(w, r, wr, id, in)
	}
}

func (a *App) nachtragEntryDelete(w http.ResponseWriter, r *http.Request, u User) {
	if wr, id, _, ok := a.nachtragPrep(w, r, u, true); ok {
		a.entryDelete(w, r, wr, id)
	}
}

func (a *App) nachtragAbsenceCreate(w http.ResponseWriter, r *http.Request, u User) {
	if wr, _, in, ok := a.nachtragPrep(w, r, u, false); ok {
		a.absenceCreate(w, r, wr, in)
	}
}

func (a *App) nachtragAbsenceUpdate(w http.ResponseWriter, r *http.Request, u User) {
	if wr, id, in, ok := a.nachtragPrep(w, r, u, true); ok {
		a.absenceUpdate(w, r, wr, id, in)
	}
}

func (a *App) nachtragAbsenceDelete(w http.ResponseWriter, r *http.Request, u User) {
	if wr, id, _, ok := a.nachtragPrep(w, r, u, true); ok {
		a.absenceDelete(w, r, wr, id)
	}
}
