package app

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"example/zeitprotokoll-server/internal/crypto"
)

/* Einstellungen je Benutzer. Jira-Token und Plane-Schlüssel (Node: settings jira_token / plane_api_key)
   liegen hier verschlüsselt in user_secrets; die Oberfläche spricht sie weiter über /api/settings an. */

var settingDefaults = map[string]string{
	"target_mon": "6.4", "target_tue": "6.4", "target_wed": "6.4", "target_thu": "6.4", "target_fri": "6.4",
	"target_sat": "0", "target_sun": "0", "opening_balance_hours": "0", "vacation_days_per_year": "30",
	"tracking_start_date": "", "jira_username": "", "suggest_enabled": "1",
}

var settingKeyRE = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// instViaSettings: Werte je Jira-Instanz, die die bisherige Oberfläche über /api/settings schickt
var instViaSettings = []string{"tempo_account", "tempo_account_attr_id", "tempo_booking_issue", "jira_new_project", "jira_new_issuetype"}

var numericSettings = []string{"target_mon", "target_tue", "target_wed", "target_thu", "target_fri", "target_sat", "target_sun", "opening_balance_hours", "vacation_days_per_year"}

// settingSecrets: Einstellungs-Schlüssel der Oberfläche → Art in user_secrets
var settingSecrets = map[string]string{"jira_token": "jira_token", "plane_api_key": "plane_key"}

func (a *App) routesSettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", a.auth(a.settingsGet))
	mux.HandleFunc("PUT /api/settings", a.auth(a.settingsPut))
}

func (a *App) userSettings(ctx context.Context, uid int64) (map[string]string, error) {
	s := make(map[string]string, len(settingDefaults))
	for k, v := range settingDefaults {
		s[k] = v
	}
	rows, err := a.DB.Query(ctx, `SELECT key, COALESCE(value,'') FROM user_settings WHERE user_id=$1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		s[k] = v
	}
	return s, rows.Err()
}

func (a *App) settingsGet(w http.ResponseWriter, r *http.Request, u User) {
	s, err := a.userSettings(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := make(map[string]any, len(s)+2)
	for k, v := range s {
		out[k] = v
	}
	for _, k := range numericSettings {
		f, _ := strconv.ParseFloat(s[k], 64)
		out[k] = f
	}
	single, serr := a.tokenInstance(r.Context(), "", true)
	for _, k := range instViaSettings { // Werte je Instanz: bei genau einer eingeschalteten deren Wert, sonst leer
		out[k] = ""
		if serr == nil {
			out[k] = s[instKey(single.ID, k)]
		}
	}
	out["plane_base_url"], out["plane_workspace"] = a.conf(r.Context(), "plane_base_url"), a.conf(r.Context(), "plane_workspace")
	for key, kind := range settingSecrets { // maskiert, nur die letzten 4 Zeichen
		var v string
		if kind == "jira_token" { // nur eindeutig bei genau einer eingeschalteten Jira-Instanz
			if inst, err := a.tokenInstance(r.Context(), "", true); err == nil {
				v, _ = a.jiraToken(r.Context(), u.ID, inst)
				if v == "" && a.hasJiraToken(r.Context(), u.ID, inst.ID) {
					v = "(nicht lesbar)" // Zeile vorhanden: maskiert zeigen, damit das Formular sie nicht leer zurückschickt und löscht
				}
			}
		} else {
			v, _ = a.Secret(r, u.ID, kind)
		}
		if v != "" {
			if len(v) > 4 {
				v = v[len(v)-4:]
			}
			out[key] = "••••" + v
		} else {
			out[key] = ""
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) settingsPut(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	if len(in) > 80 {
		badRequest(w, "Zu viele Einstellungen auf einmal")
		return
	}
	ctx := r.Context()
	// Jira-Werte je Instanz: über diese Route nur eindeutig bei genau einer eingeschalteten Instanz
	single, singleErr := a.tokenInstance(ctx, "", true)
	for key, val := range in { // erst prüfen, dann schreiben (kein Teil-Ergebnis)
		sv := strings.TrimSpace(fmt.Sprint(val))
		if val == nil || strings.HasPrefix(sv, "••••") {
			continue
		}
		if (key == "jira_token" || contains(instViaSettings, key)) && sv != "" && singleErr != nil {
			badRequest(w, key+": "+singleErr.Error()+" – je Jira-Instanz setzen (/api/jira/instances)")
			return
		}
	}
	for key, val := range in {
		sv := fmt.Sprint(val)
		if val == nil {
			sv = ""
		}
		if key == "jira_base_url" { // Jira-Adressen legt der Administrator als Jira-Instanzen fest: still übergehen
			continue
		}
		if contains(instViaSettings, key) {
			if singleErr != nil {
				continue // leer bei mehreren Instanzen: nichts zu tun (oben geprüft)
			}
			key = instKey(single.ID, key)
			if _, err := a.DB.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,$2,$3)
				ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, u.ID, key, strings.TrimSpace(sv)); err != nil {
				a.fail(w, err)
				return
			}
			continue
		}
		if key == "plane_me_id" || strings.HasPrefix(key, "plane_me_id:") || key == "gapfill_ok" || key == "gaps_todo_dismissed" { // wird nur vom Server gepflegt (gapfill_ok: Monatssperre in /month-end/ok)
			continue
		}
		if key == "plane_base_url" || key == "plane_workspace" { // gelten für alle: nur über /api/admin/config (Administrator, protokolliert)
			if strings.TrimSpace(sv) != a.conf(ctx, key) {
				badRequest(w, "Plane-Adresse und Workspace ändert nur der Administrator unter /admin (Servereinstellung)")
				return
			}
			continue
		}
		if !settingKeyRE.MatchString(key) || len(sv) > 4096 {
			badRequest(w, "Einstellung ungültig: "+key)
			return
		}
		if kind, ok := settingSecrets[key]; ok {
			if strings.HasPrefix(sv, "••••") {
				continue // maskierter Wert → unverändert lassen
			}
			if kind == "jira_token" { // Instanz: nur eindeutig bei genau einer eingeschalteten, sonst Profil-Route mit ?instance=
				if singleErr != nil {
					continue // leer bei mehreren Instanzen: nichts löschen (oben geprüft)
				}
				inst := single
				var err error
				if strings.TrimSpace(sv) == "" {
					err = a.deleteJiraToken(ctx, u.ID, inst.ID)
				} else {
					err = a.setJiraToken(ctx, u.ID, inst, strings.TrimSpace(sv))
				}
				if err != nil {
					a.fail(w, err)
					return
				}
				a.audit(ctx, u.ID, map[bool]string{true: "secret.delete", false: "secret.set"}[strings.TrimSpace(sv) == ""], fmt.Sprintf("jira_token (Instanz %d)", inst.ID))
				continue
			}
			if strings.TrimSpace(sv) == "" {
				if _, err := a.DB.Exec(ctx, `DELETE FROM user_secrets WHERE user_id=$1 AND kind=$2 AND scope=0`, u.ID, kind); err != nil {
					a.fail(w, err)
					return
				}
				a.clearPlaneMe(ctx, u.ID, kind)
				a.audit(ctx, u.ID, "secret.delete", kind)
				continue
			}
			blob, err := crypto.SealAAD(a.Key, []byte(strings.TrimSpace(sv)), a.secretAADFor(ctx, u.ID, kind))
			if err != nil {
				a.fail(w, err)
				return
			}
			if _, err := a.DB.Exec(ctx, `INSERT INTO user_secrets(user_id,kind,ciphertext) VALUES($1,$2,$3)
				ON CONFLICT (user_id,kind,scope) DO UPDATE SET ciphertext=EXCLUDED.ciphertext, updated_at=now()`, u.ID, kind, blob); err != nil {
				a.fail(w, err)
				return
			}
			a.clearPlaneMe(ctx, u.ID, kind)
			a.audit(ctx, u.ID, "secret.set", kind)
			continue
		}
		if _, err := a.DB.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,$2,$3)
			ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, u.ID, key, sv); err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// clearPlaneMe: bei neuem/gelöschtem Plane-Schlüssel die gemerkte Plane-Benutzer-Id verwerfen (sie gehört zum alten Schlüssel)
func (a *App) clearPlaneMe(ctx context.Context, uid int64, kind string) {
	if kind == "plane_key" {
		_, _ = a.DB.Exec(ctx, `DELETE FROM user_settings WHERE user_id=$1 AND (key='plane_me_id' OR key LIKE 'plane_me_id:%')`, uid)
	}
}
