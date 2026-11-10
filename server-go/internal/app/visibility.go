package app

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

/* Sichtbarkeit: Mitarbeiter sehen sich selbst, Teamleiter die Mitglieder ihrer Teams, Personal (is_hr) alle.
   Fachrollen sind kombinierbar; der technische Administrator ist ein getrenntes Konto und sieht KEINE Zeitdaten.
   Fremde Daten gibt es nur lesend über „?user=<id>“ an ausgewählten GET-Routen (asTarget); alles Schreibende bleibt eigene Daten. */

// canSee: darf viewer die Zeitdaten von target lesen?
func (a *App) canSee(ctx context.Context, viewer User, target int64) bool {
	if viewer.ID == target {
		return true
	}
	if viewer.Role == "breakglass" {
		return false
	}
	if viewer.IsHR {
		return true
	}
	if !viewer.IsLead {
		return false
	}
	var ok bool
	err := a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM team_members m1 JOIN team_members m2 ON m1.team_id=m2.team_id
		WHERE m1.user_id=$1 AND m1.is_lead AND m2.user_id=$2)`, viewer.ID, target).Scan(&ok)
	return err == nil && ok
}

var seenMu sync.Mutex
var seen = map[string]time.Time{}

// auditViewOnce protokolliert den Blick auf fremde Daten höchstens einmal je Person und Stunde
func (a *App) auditViewOnce(ctx context.Context, viewer, target int64) {
	k := strconv.FormatInt(viewer, 10) + ">" + strconv.FormatInt(target, 10)
	seenMu.Lock()
	t, ok := seen[k]
	if ok && time.Since(t) < time.Hour {
		seenMu.Unlock()
		return
	}
	if len(seen) > 5000 {
		seen = map[string]time.Time{}
	}
	seen[k] = time.Now()
	seenMu.Unlock()
	a.audit(ctx, viewer, "view.other", "Zeitdaten von Person "+strconv.FormatInt(target, 10)+" angesehen")
}

// asTarget lässt eine reine Lese-Route auf die Daten einer anderen Person zeigen (?user=<id>), wenn die Sichtbarkeit es erlaubt.
// Der Handler bekommt eine Benutzerkopie ohne Sonderrechte; Unberechtigte erhalten 404 (keine Auskunft, ob es die Person gibt).
func (a *App) asTarget(h func(http.ResponseWriter, *http.Request, User)) func(http.ResponseWriter, *http.Request, User) {
	return func(w http.ResponseWriter, r *http.Request, u User) {
		s := r.URL.Query().Get("user")
		if s == "" {
			h(w, r, u)
			return
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			badRequest(w, "user ungültig")
			return
		}
		if id == u.ID {
			h(w, r, u)
			return
		}
		if !a.canSee(r.Context(), u, id) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
			return
		}
		t := User{ID: id, Role: "user", Kind: "viewed"}
		if err := a.DB.QueryRow(r.Context(), `SELECT email, name FROM users WHERE id=$1 AND role<>'breakglass'`, id).Scan(&t.Email, &t.Name); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
			return
		}
		a.auditViewOnce(r.Context(), u.ID, id)
		h(w, r, t)
	}
}
