package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"example/zeitprotokoll-server/internal/crypto"
	"example/zeitprotokoll-server/internal/db"
)

/* Selbsttest des Servers: beim Start, alle 6 Stunden, per Admin-Oberfläche und per Befehl (server check).
   Sichere Reparaturen (fix) macht er selbst: fehlende Migrationen nachziehen, Abgelaufenes aufräumen.
   Alles, was Daten verwerfen würde (force), geschieht nur auf ausdrücklichen Befehl (server check --fix). */

type CheckItem struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | error | fixed
	Detail string `json:"detail"`
}

type CheckOpts struct{ Fix, Force bool }

type CheckReport struct {
	At    string      `json:"at"`
	Items []CheckItem `json:"items"`
	Worst string      `json:"worst"`
}

var requiredTables = []string{"users", "sessions", "device_tokens", "user_secrets", "user_settings", "server_config", "audit_log", "projects", "tasks",
	"plan_tasks", "time_entries", "timer_state", "pause_state", "pauses", "daily_logs", "absences", "month_closings", "balance_corrections",
	"system_logs", "plane_deletions", "plane_ticket_sync", "project_sources", "presence_min", "calendar_events", "meeting_state", "entry_audit", "jira_instances"}

func rank(s string) int { return map[string]int{"ok": 0, "fixed": 1, "warn": 2, "error": 3}[s] }

// SelfCheck führt alle Prüfungen aus und merkt sich das Ergebnis (für die Admin-Oberfläche).
func (a *App) SelfCheck(ctx context.Context, o CheckOpts) CheckReport {
	var items []CheckItem
	add := func(name, status, detail string) { items = append(items, CheckItem{name, status, detail}) }

	// 1) Datenbank
	var ver string
	if err := a.DB.QueryRow(ctx, `SELECT version()`).Scan(&ver); err != nil {
		add("Datenbank", "error", "nicht erreichbar: "+err.Error())
		return a.finish(items)
	}
	add("Datenbank", "ok", strings.SplitN(ver, " on ", 2)[0])

	// 2) Migrationen
	applied := map[string]bool{}
	if rows, err := a.DB.Query(ctx, `SELECT name FROM schema_migrations`); err == nil {
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				applied[n] = true
			}
		}
		rows.Close()
	}
	var missing []string
	for _, n := range db.MigrationNames() {
		if !applied[n] {
			missing = append(missing, n)
		}
		delete(applied, n)
	}
	switch {
	case len(missing) > 0 && o.Fix:
		if done, err := db.Migrate(ctx, a.DB); err != nil {
			add("Migrationen", "error", "Nachziehen fehlgeschlagen: "+err.Error())
		} else {
			add("Migrationen", "fixed", "nachgezogen: "+strings.Join(done, ", "))
		}
	case len(missing) > 0:
		add("Migrationen", "error", "fehlen: "+strings.Join(missing, ", ")+" (server migrate bzw. check --fix)")
	case len(applied) > 0:
		var extra []string
		for n := range applied {
			extra = append(extra, n)
		}
		add("Migrationen", "warn", "Datenbank ist neuer als dieser Server: "+strings.Join(extra, ", ")+" – Programm aktualisieren?")
	default:
		add("Migrationen", "ok", fmt.Sprintf("%d angewendet", len(db.MigrationNames())))
	}

	// 3) Tabellen vorhanden
	have := map[string]bool{}
	if rows, err := a.DB.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()`); err == nil {
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				have[n] = true
			}
		}
		rows.Close()
	}
	var gone []string
	for _, t := range requiredTables {
		if !have[t] {
			gone = append(gone, t)
		}
	}
	if len(gone) > 0 {
		add("Tabellen", "error", "fehlen: "+strings.Join(gone, ", ")+" – aus der Sicherung wiederherstellen")
	} else {
		add("Tabellen", "ok", fmt.Sprintf("%d Pflichttabellen vorhanden", len(requiredTables)))
	}

	// 4) Schlüssel zu den gespeicherten Zugangsdaten
	a.checkSecrets(ctx, o, add)
	a.checkTempoMonth(ctx, add)
	a.checkBypassGUC(ctx, add)

	// 5) Datenstimmigkeit (nur melden)
	a.checkIntegrity(ctx, add)

	// 6) Administrator und Einrichtung
	var admins int
	_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND NOT disabled`).Scan(&admins)
	switch {
	case a.conf(ctx, "setup_done") != "1":
		add("Einrichtung", "warn", "noch nicht eingerichtet (/setup)")
	case admins == 0:
		add("Administrator", "error", "kein aktiver Administrator – server user set-role <E-Mail> admin")
	default:
		add("Administrator", "ok", fmt.Sprintf("%d aktiv", admins))
	}
	a.checkAccounts(ctx, add)
	if a.conf(ctx, "auth_mode") == "none" {
		if a.Production {
			add("Anmeldung", "error", "Testmodus ohne Anmeldung im Betrieb – wird abgelehnt; OIDC einrichten")
		} else {
			add("Anmeldung", "warn", "Testmodus ohne Anmeldung – nur für lokale Tests, nicht für den Betrieb")
		}
	}
	if a.KeyFromFile {
		add("Schlüssel", "warn", "ZP_SECRET_KEY fehlt – Schlüsseldatei im Datenordner (nur Test); im Betrieb aus einem Secret setzen")
	}
	if base := a.conf(ctx, "plane_base_url"); base != "" {
		if _, err := a.validatePlaneURL(base); err != nil {
			add("Plane", "warn", err.Error())
		}
	}

	// 7) Aufräumen (sichere Reparatur)
	if o.Fix {
		a.cleanup(ctx, add)
	}
	return a.finish(items)
}

func (a *App) finish(items []CheckItem) CheckReport {
	worst := "ok"
	for _, i := range items {
		if rank(i.Status) > rank(worst) {
			worst = i.Status
		}
	}
	rep := CheckReport{At: time.Now().UTC().Format(time.RFC3339), Items: items, Worst: worst}
	a.lastCheck.Store(&rep)
	return rep
}

func (a *App) checkSecrets(ctx context.Context, o CheckOpts, add func(string, string, string)) {
	insts := map[int64]jiraInstance{}
	if list, err := a.JiraInstances(ctx); err == nil {
		for _, i := range list {
			insts[i.ID] = i
		}
	}
	rows, err := a.DB.Query(ctx, `SELECT user_id, kind, scope, ciphertext FROM user_secrets`)
	if err != nil {
		add("Zugangsdaten", "error", err.Error())
		return
	}
	type secret struct {
		uid   int64
		kind  string
		scope int64
		plain []byte
	}
	var upgrade, broken []secret
	var total, orphan int
	// aadFor: aktuelle Bindung; Jira-Token je Instanz an Person, Instanz und Schema+Host gebunden
	aadFor := func(uid int64, kind string, scope int64) ([]byte, bool) {
		if kind != "jira_token" {
			return a.secretAADFor(ctx, uid, kind), true
		}
		inst, ok := insts[scope]
		if !ok {
			return nil, false
		}
		return jiraTokenAAD(uid, inst.ID, inst.BaseURL), true
	}
	for rows.Next() {
		var x secret
		var blob []byte
		if rows.Scan(&x.uid, &x.kind, &x.scope, &blob) != nil {
			continue
		}
		total++
		aad, ok := aadFor(x.uid, x.kind, x.scope)
		if !ok { // Jira-Token ohne (bestehende) Instanz: nicht benutzbar
			orphan++
			broken = append(broken, x)
			continue
		}
		plain, err := crypto.OpenAAD(a.Key, blob, aad)
		if err == nil && !crypto.IsLegacy(blob) {
			continue
		}
		if err != nil && x.kind == "jira_token" { // vor Migration 014 nur an Person/Art gebunden
			plain, err = crypto.OpenAAD(a.Key, blob, secretAAD(x.uid, x.kind))
		}
		if err != nil {
			broken = append(broken, x)
			continue
		}
		x.plain = plain
		upgrade = append(upgrade, x)
	}
	rows.Close()
	if o.Fix && len(upgrade) > 0 { // altes Format → neu verschlüsseln, jetzt an Person/Art (und Instanz/Host) gebunden
		n := 0
		for _, u := range upgrade {
			aad, _ := aadFor(u.uid, u.kind, u.scope)
			if blob, err := crypto.SealAAD(a.Key, u.plain, aad); err == nil {
				if _, err := a.DB.Exec(ctx, `UPDATE user_secrets SET ciphertext=$4, updated_at=now() WHERE user_id=$1 AND kind=$2 AND scope=$3`, u.uid, u.kind, u.scope, blob); err == nil {
					n++
				}
			}
		}
		if n > 0 {
			add("Zugangsdaten", "fixed", fmt.Sprintf("%d im alten Format neu verschlüsselt (an Person/Art/Instanz gebunden)", n))
		}
	}
	switch {
	case len(broken) == 0:
		add("Zugangsdaten", "ok", fmt.Sprintf("%d gespeicherte Zugangsdaten lesbar", total))
	case o.Force:
		for _, b := range broken {
			_, _ = a.DB.Exec(ctx, `DELETE FROM user_secrets WHERE user_id=$1 AND kind=$2 AND scope=$3`, b.uid, b.kind, b.scope)
		}
		add("Zugangsdaten", "fixed", fmt.Sprintf("%d nicht lesbare gelöscht – die Personen tragen sie im Profil neu ein", len(broken)))
	case orphan == len(broken):
		add("Zugangsdaten", "warn", fmt.Sprintf("%d Jira-Tokens ohne Jira-Instanz (vor der Umstellung ohne Adresse) – nicht benutzbar; mit check --force löschen", orphan))
	default:
		add("Zugangsdaten", "error", fmt.Sprintf("%d von %d nicht lesbar (davon %d ohne Jira-Instanz) – ZP_SECRET_KEY geändert? Alten Schlüssel setzen oder mit check --force löschen (Personen tragen sie neu ein)", len(broken), total, orphan))
	}
}

func (a *App) checkIntegrity(ctx context.Context, add func(string, string, string)) {
	count := func(sql string) int64 {
		var n int64
		_ = a.DB.QueryRow(ctx, sql).Scan(&n)
		return n
	}
	foreign := count(`SELECT (SELECT COUNT(*) FROM time_entries e JOIN projects p ON p.id=e.project_id WHERE p.user_id<>e.user_id)
		+ (SELECT COUNT(*) FROM time_entries e JOIN tasks t ON t.id=e.task_id WHERE t.user_id<>e.user_id)
		+ (SELECT COUNT(*) FROM tasks t JOIN projects p ON p.id=t.project_id WHERE p.user_id<>t.user_id)
		+ (SELECT COUNT(*) FROM plan_tasks t JOIN projects p ON p.id=t.project_id WHERE p.user_id<>t.user_id)`)
	if foreign > 0 {
		add("Daten: Zuordnung", "error", fmt.Sprintf("%d Datensätze verweisen auf Daten einer anderen Person – nicht automatisch reparierbar, Sicherung/Import prüfen", foreign))
	} else {
		add("Daten: Zuordnung", "ok", "keine Verweise zwischen Personen")
	}
	neg := count(`SELECT COUNT(*) FROM time_entries WHERE duration_seconds < 0`)
	if neg > 0 {
		add("Daten: Dauer", "warn", fmt.Sprintf("%d Einträge mit negativer Dauer", neg))
	} else {
		add("Daten: Dauer", "ok", "keine negativen Dauern")
	}
	old := count(`SELECT COUNT(*) FROM timer_state WHERE start_time IS NOT NULL AND start_time <> '' AND start_time < '` + time.Now().Add(-24*time.Hour).UTC().Format(isoMillis) + `'`)
	if old > 0 {
		add("Timer", "warn", fmt.Sprintf("%d Timer laufen seit über 24 Stunden (vergessen?)", old))
	}
}

// cleanup: Abgelaufenes aufräumen (immer sicher)
func (a *App) cleanup(ctx context.Context, add func(string, string, string)) {
	var parts []string
	del := func(label, sql string, args ...any) {
		if tag, err := a.DB.Exec(ctx, sql, args...); err == nil && tag.RowsAffected() > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", label, tag.RowsAffected()))
		}
	}
	del("Sitzungen", `DELETE FROM sessions WHERE expires_at < now()`)
	del("Anmelde-Vorgänge", `DELETE FROM oidc_flows WHERE expires_at < now()`)
	del("Logs", `DELETE FROM system_logs WHERE timestamp < $1`, time.Now().Add(-30*24*time.Hour).UTC().Format(isoMillis))
	del("Anwesenheit", `DELETE FROM presence_min WHERE date < $1`, time.Now().Add(-120*24*time.Hour).In(a.Loc).Format("2006-01-02"))
	del("Kalender", `DELETE FROM calendar_events WHERE date < $1`, time.Now().Add(-60*24*time.Hour).In(a.Loc).Format("2006-01-02"))
	if len(parts) > 0 {
		add("Aufräumen", "fixed", "entfernt: "+strings.Join(parts, ", "))
	} else {
		add("Aufräumen", "ok", "nichts Abgelaufenes")
	}
}

// StartSelfCheck: einmal beim Start, danach alle 6 Stunden (sichere Reparaturen inklusive); Ergebnis im Log
// safely führt eine Hintergrundaufgabe aus und fängt einen Absturz ab (sonst würde er den ganzen Server beenden)
func (a *App) safely(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			a.Log.Error("Absturz in Hintergrundaufgabe abgefangen", "aufgabe", name, "fehler", fmt.Sprint(r))
		}
	}()
	fn()
}

func (a *App) StartSelfCheck(ctx context.Context) {
	a.clearPendingClaims(ctx)  // nur beim Start: dann läuft noch kein Anlegen
	a.startTempoCron(ctx)      // Tempo-Automatik alle 10 min (nur bei module_tempo=1)
	a.startTempoMonthCron(ctx) // Übergangs-Tickets alle 6 h schließen/wiederöffnen
	a.startMonthCloseCron(ctx) // Monatsschluss nach Nachfrist: beim Start und alle 15 min
	run := func() {
		purgeCaches()
		rep := a.SelfCheck(ctx, CheckOpts{Fix: true})
		for _, i := range rep.Items {
			switch i.Status {
			case "error":
				a.Log.Error("Selbsttest", "prüfung", i.Name, "detail", i.Detail)
			case "warn":
				a.Log.Warn("Selbsttest", "prüfung", i.Name, "detail", i.Detail)
			case "fixed":
				a.Log.Info("Selbsttest: repariert", "prüfung", i.Name, "detail", i.Detail)
			}
		}
		a.Log.Info("Selbsttest abgeschlossen", "ergebnis", rep.Worst)
	}
	a.safely("selbsttest", run)
	go func() { // Notfallzugang überwachen: Ablauf zurücknehmen, Freigabe laut melden
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			a.safely("notfall-waechter", func() { a.breakGlassTick(ctx) })
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	go func() { // Verzeichnis nachprüfen: Sperrung/Gruppenwechsel greift spätestens nach 30 min
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.safely("ldap-nachpruefung", func() { a.ldapRecheckTick(ctx) })
			}
		}
	}()
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.safely("selbsttest", run)
			}
		}
	}()
}

/* ── Routen (Admin) ───────────────────────────────────────────────── */

func (a *App) adminSelfcheck(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if r.Method == http.MethodPost && r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	fix, _ := in["fix"].(bool)
	force := false // Zugangsdaten löschen geht nur per Befehl (server check --force), nie über die Weboberfläche
	if r.Method == http.MethodGet {
		if rep := a.lastCheck.Load(); rep != nil && r.URL.Query().Get("fresh") != "1" {
			writeJSON(w, http.StatusOK, rep)
			return
		}
	}
	rep := a.SelfCheck(r.Context(), CheckOpts{Fix: fix, Force: force})
	if fix {
		a.audit(r.Context(), u.ID, "admin.selfcheck", fmt.Sprintf("fix, Ergebnis %s", rep.Worst))
	}
	writeJSON(w, http.StatusOK, rep)
}

// checkAccounts: Konten passend zur Anmeldeart (Einzelbetrieb, lokaler Administrator, Notfallzugang)
func (a *App) checkAccounts(ctx context.Context, add func(string, string, string)) {
	count := func(sql string) int64 {
		var n int64
		_ = a.DB.QueryRow(ctx, sql).Scan(&n)
		return n
	}
	mode := a.conf(ctx, "auth_mode")
	localProd := count(`SELECT COUNT(*) FROM users WHERE kind='local' AND role<>'breakglass' AND productive AND NOT disabled`)
	localAdmin := count(`SELECT COUNT(*) FROM users WHERE kind='local' AND role='admin' AND NOT productive AND NOT disabled`)
	extProd := count(`SELECT COUNT(*) FROM users WHERE kind<>'local' AND NOT disabled`)
	noPw := count(`SELECT COUNT(*) FROM users WHERE kind='local' AND NOT disabled AND password_hash IS NULL`)
	switch mode {
	case "local":
		if localProd > 1 {
			add("Einzelbetrieb", "error", fmt.Sprintf("%d produktive lokale Personen – erlaubt ist eine. Weitere Mitarbeitende brauchen authentik/LDAP", localProd))
		} else {
			add("Einzelbetrieb", "ok", fmt.Sprintf("%d produktive lokale Person (erlaubt: 1)", localProd))
		}
	case "oidc", "ldap":
		if localAdmin > 0 && extProd > 1 {
			add("Lokaler Administrator", "error", "Weitere Mitarbeitende arbeiten über das Verzeichnis, der lokale Test-Administrator ist aber noch aktiv – entfernen (Admin-Oberfläche oder server user remove)")
		} else if localAdmin > 0 {
			add("Lokaler Administrator", "warn", "Das lokale Test-Konto ist noch aktiv – entfernen, sobald weitere Mitarbeitende angebunden sind")
		}
	}
	if noPw > 0 {
		add("Konten", "warn", fmt.Sprintf("%d lokale Konten ohne Passwort können sich nicht anmelden (server user reset-password)", noPw))
	}
	var armed *time.Time
	hasBG := count(`SELECT COUNT(*) FROM users WHERE role='breakglass'`) > 0
	_ = a.DB.QueryRow(ctx, `SELECT armed_until FROM users WHERE role='breakglass' AND armed_until > now() LIMIT 1`).Scan(&armed)
	switch {
	case armed != nil:
		add("Notfallzugang", "warn", "SCHARF geschaltet bis "+armed.In(a.Loc).Format("02.01. 15:04")+" Uhr")
	case !hasBG && (mode == "oidc" || mode == "ldap" || a.Production):
		add("Notfallzugang", "warn", "Es gibt keinen Notfallzugang – anlegen mit: server breakglass create (Passwort in den Tresor)")
	case hasBG:
		add("Notfallzugang", "ok", "vorhanden, gesperrt")
	}
}

// checkTempoMonth: Übergangs-Tickets, die sich nicht schließen/wiederöffnen ließen (wird alle 6 h wiederholt)
func (a *App) checkTempoMonth(ctx context.Context, add func(string, string, string)) {
	var n int64
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM tempo_month_tickets WHERE close_error IS NOT NULL`).Scan(&n); err != nil {
		return
	}
	if n > 0 {
		add("Jira: Übergangs-Tickets", "warn", fmt.Sprintf("%d ließen sich nicht schließen/wieder öffnen – wird alle 6 h wiederholt (Details im Log, Kategorie tempo)", n))
	}
}
