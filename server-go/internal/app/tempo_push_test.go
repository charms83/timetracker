package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"example/zeitprotokoll-server/internal/crypto"
)

func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("Zeitzonendaten fehlen")
	}
	return loc
}

// Erwartungswerte erzeugt mit den Funktionen aus backend/tempo-push.js (TZ=Europe/Berlin) – Hash muss gleich bleiben
func TestTempoPlanNodeCompat(t *testing.T) {
	loc := berlin(t)
	base := tempoCfg{PlaneMode: "month", JiraKeys: map[string]bool{}}
	cfg := func(f func(*tempoCfg)) tempoCfg {
		c := base
		c.JiraKeys = map[string]bool{}
		f(&c)
		return c
	}
	cases := []struct {
		name string
		c    tempoCfg
		lk   tempoLookup
		e    tempoEntry
		want [8]any // target, kind, secs, started, comment, account, from, hash
	}{
		{"tempo_target", base, tempoLookup{}, tempoEntry{TempoTarget: " abc-12 ", TaskID: 1, Ticket: "PRJA-1", PlaneWI: "wi", Duration: 3599, StartTime: "2026-10-05T07:00:00.000Z", Comment: "  Test  "},
			[8]any{"ABC-12", "ticket", int64(3600), "2026-10-05T09:00:00.000", "Test", "", "Ticket", "e11eb8011ca0d819"}},
		{"eigenes_jira_link", cfg(func(c *tempoCfg) { c.Account = "STD" }), tempoLookup{}, tempoEntry{TaskID: 1, JiraLink: "ZEIT-5", Ticket: "x", Duration: 29, StartTime: "2026-10-31T23:00:00.000Z", TaskTitle: "Titel\n zwei"},
			[8]any{"ZEIT-5", "ticket", int64(60), "2026-11-01T00:00:00.000", "Titel zwei", "", "Ticket", "c22f99ca5626c992"}},
		{"eigenes_jira_keys", cfg(func(c *tempoCfg) { c.Account = "STD"; c.JiraKeys["OPS"] = true }), tempoLookup{}, tempoEntry{TaskID: 1, Ticket: "ops-7", Duration: 90, StartTime: "2026-03-29T00:30:00", TempoAccount: "KUNDE"},
			[8]any{"OPS-7", "ticket", int64(120), "2026-03-29T00:30:00.000", "Arbeit", "KUNDE", "Buchung", "f28e79741d59ce5f"}},
		// Hash wie Node JSON.stringify (U+2028 bleibt Zeichen, wird nicht als \\u2028 escaped) – Abgleich 10.11.2026
		{"plane_month", cfg(func(c *tempoCfg) { c.PlaneURL = "https://plane.example/hon"; c.Worker = "harm" }), tempoLookup{MonthTickets: map[string]string{"2026-10": "ZEIT-99"}},
			tempoEntry{TaskID: 2, Ticket: "PRJA-7", TaskTitle: "Tür \"x\" ü\\ <a>&", PlaneWI: "wi", ProjectID: 3, ProjectName: "Projekt A", Date: "2026-10-06", Duration: 1800, StartTime: "2026-10-06T00:00:00", Comment: "a\u2028b\tc", ProjAccount: "PA"},
			[8]any{"ZEIT-99", "month", int64(1800), "2026-10-06T00:00:00.000", "[PRJA-7] Tür \"x\" ü\\ <a>& – a b c · Projekt A · Plane: https://plane.example/hon/browse/PRJA-7/", "PA", "Projekt", "cc559cad2bd3e258"}},
		{"plane_month_create", cfg(func(c *tempoCfg) { c.NewProject = "ZEIT"; c.Worker = "harm" }), tempoLookup{}, tempoEntry{TaskID: 2, Ticket: "PRJA-7", PlaneWI: "wi", ProjectID: 3, Date: "2026-11-02", Duration: 61, StartTime: "2026-11-02T08:15:31"},
			[8]any{"NEU:ZEIT Monat 2026-11", "create-month", int64(60), "2026-11-02T08:15:31.000", "[PRJA-7]", "", "", "34094b0bbd18e412"}},
		{"plane_month_prev_target", cfg(func(c *tempoCfg) { c.Worker = "harm" }), tempoLookup{MonthTickets: map[string]string{"2026-10": "ZEIT-99"}},
			tempoEntry{TaskID: 2, Ticket: "PRJA-7", PlaneWI: "wi", ProjectID: 3, Date: "2026-10-06", PushID: "123", PushTarget: "ZEIT-50", Duration: 600, StartTime: "2026-10-06T10:00:00"},
			[8]any{"ZEIT-50", "ticket", int64(600), "2026-10-06T10:00:00.000", "Arbeit", "", "Ticket", "ba5ef4d407c15208"}},
		{"ticket_mode_mapped", cfg(func(c *tempoCfg) { c.PlaneMode = "ticket"; c.NewProject = "ZEIT" }), tempoLookup{Mapped: map[string]string{"PRJA-7": "old-3"}},
			tempoEntry{TaskID: 2, Ticket: "prja-7", PlaneWI: "wi", ProjectID: 3, Date: "2026-10-06", Duration: 600, StartTime: "2026-10-06T10:00:00"},
			[8]any{"OLD-3", "ticket", int64(600), "2026-10-06T10:00:00.000", "Arbeit", "", "Ticket", "81c0bdb27f285e18"}},
		{"ticket_mode_create", cfg(func(c *tempoCfg) { c.PlaneMode = "ticket"; c.NewProject = "ZEIT" }), tempoLookup{},
			tempoEntry{TaskID: 2, Ticket: "PRJA-8", PlaneWI: "wi", ProjectID: 3, Date: "2026-10-06", Duration: 600, StartTime: "2026-10-06T10:00:00"},
			[8]any{"NEU:ZEIT", "create", int64(600), "2026-10-06T10:00:00.000", "Arbeit", "", "", "b7981b541459907a"}},
		{"ticket_mode_collector", cfg(func(c *tempoCfg) { c.PlaneMode = "ticket"; c.Issue = "SAMMEL-1"; c.Account = "STD" }), tempoLookup{},
			tempoEntry{TaskID: 2, PlaneWI: "wi", ProjectID: 3, ProjectName: "P", Date: "2026-10-06", Duration: 600, StartTime: "2026-10-06T10:00:00", Comment: "Notiz"},
			[8]any{"SAMMEL-1", "collector", int64(600), "2026-10-06T10:00:00.000", "[P] Notiz", "STD", "Standard", "56a6cce43f0ae880"}},
		{"no_task_plane_project_collector", cfg(func(c *tempoCfg) { c.PlaneMode = "ticket"; c.Account = "STD" }), tempoLookup{},
			tempoEntry{ProjectID: 4, AllowsPlane: true, ProjIssue: " proj-2 ", ProjectName: "Plane-Projekt", Date: "2026-10-06", Duration: 600, StartTime: "2026-10-06T10:00:00", Comment: "x"},
			[8]any{"PROJ-2", "collector", int64(600), "2026-10-06T10:00:00.000", "[Plane-Projekt] x", "STD", "Standard", "789b681505eb95c1"}},
		// Regel 29.10.2026: keine Jira-Buchung ohne Ticket (kein Plane → kein Sammelticket, auch wenn eines eingestellt ist)
		{"no_task_no_plane", cfg(func(c *tempoCfg) { c.Issue = "SAMMEL-1"; c.NewProject = "ZEIT"; c.Account = "STD" }), tempoLookup{},
			tempoEntry{ProjectID: 4, ProjectName: "Lokal", Date: "2026-10-06", Duration: 600, StartTime: "2026-10-06T10:00:00"},
			[8]any{"", "no-ticket", int64(600), "2026-10-06T10:00:00.000", "Arbeit", "STD", "Standard", "e798776ae17def93"}},
		{"local_task_no_jira", cfg(func(c *tempoCfg) { c.Issue = "SAMMEL-1"; c.Account = "STD" }), tempoLookup{},
			tempoEntry{TaskID: 5, Ticket: "ABC-1", ProjectID: 4, Date: "2026-10-06", Duration: 600, StartTime: "2026-10-06T10:00:00"},
			[8]any{"", "no-ticket", int64(600), "2026-10-06T10:00:00.000", "Arbeit", "STD", "Standard", "e798776ae17def93"}},
		{"plane_create_collector", cfg(func(c *tempoCfg) { c.PlaneMode = "ticket"; c.NewProject = "ZEIT" }), tempoLookup{},
			tempoEntry{ProjectID: 4, AllowsPlane: true, Date: "2026-10-06", Duration: 600, StartTime: "2026-10-06T10:00:00"},
			[8]any{"NEU:ZEIT", "create-collector", int64(600), "2026-10-06T10:00:00.000", "Arbeit", "", "", "b7981b541459907a"}},
	}
	for _, tc := range cases {
		p := tempoPlanOf(tc.e, tc.c, tc.lk, loc)
		got := [8]any{p.Target, p.Kind, p.Secs, p.Started, p.Comment, p.Account, p.AccountFrom, p.Hash}
		if got != tc.want {
			t.Errorf("%s:\n got  %#v\n want %#v", tc.name, got, tc.want)
		}
	}
}

func TestTempoCommentCut(t *testing.T) {
	loc := berlin(t)
	e := tempoEntry{TaskID: 1, JiraLink: "ZEIT-5", Duration: 600, StartTime: "2026-10-06T10:00:00", Comment: strings.Repeat("Wort  ", 200)}
	p := tempoPlanOf(e, tempoCfg{}, tempoLookup{}, loc)
	if len(p.Comment) != 900 || p.Hash != "b309e1381e198089" { // Node: gleicher Hash
		t.Errorf("Kürzung 900: len %d hash %s", len(p.Comment), p.Hash)
	}
	// Ersatzpaar an Stelle 900: Node schneidet es halb (einsames Surrogat), Go lässt es ganz weg
	e.Comment = strings.Repeat("x", 899) + "😀y"
	if c := tempoPlanOf(e, tempoCfg{}, tempoLookup{}, loc).Comment; c != strings.Repeat("x", 899) {
		t.Errorf("Emoji an der Grenze: len %d", len(c))
	}
	long := tempoEntry{Ticket: "AB-1", TaskTitle: strings.Repeat("ä", 1000)}
	if d := tempoDescribe(long); len([]rune(d)) != 900 {
		t.Errorf("describe ≤ 900: %d", len([]rune(d)))
	}
}

func TestTempoDescribe(t *testing.T) {
	for _, tc := range []struct {
		e    tempoEntry
		want string
	}{
		{tempoEntry{}, "Arbeit"},
		{tempoEntry{ProjectName: "Intern"}, "[Intern]"},
		{tempoEntry{Ticket: " prja-1 ", TaskTitle: "PRJA-1", Comment: "Text"}, "[PRJA-1] Text"}, // Titel gleich Key entfällt
		{tempoEntry{Ticket: "PRJA-1", TaskTitle: "Login", Comment: " a\n\nb "}, "[PRJA-1] Login – a b"},
		{tempoEntry{ProjectName: "P", TaskTitle: "Nur Titel"}, "[P] Nur Titel"},
	} {
		if got := tempoDescribe(tc.e); got != tc.want {
			t.Errorf("describe(%+v) = %q, want %q", tc.e, got, tc.want)
		}
	}
}

func TestTempoSecsAndStamp(t *testing.T) {
	for in, want := range map[int64]int64{1: 60, 29: 60, 30: 60, 89: 60, 90: 120, 3599: 3600, 3630: 3660, 3629: 3600} {
		if got := tempoSecs(in); got != want {
			t.Errorf("tempoSecs(%d) = %d, want %d", in, got, want)
		}
	}
	loc := berlin(t)
	for in, want := range map[string]string{
		"2026-10-06T00:00:00":      "2026-10-06T00:00:00.000", // 00:00-Start bleibt 00:00
		"2026-10-05T22:00:00.000Z": "2026-10-06T00:00:00.000", // UTC → lokale Zeit, Datum springt
		"2026-10-25T01:30:00Z":     "2026-10-25T02:30:00.000", // nach Sommerzeit-Ende (Winterzeit)
		"2026-10-06T08:15:31.987":  "2026-10-06T08:15:31.000", // Millisekunden immer .000
		"kaputt":                   "",
	} {
		if got := tempoStamp(in, loc); got != want {
			t.Errorf("tempoStamp(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTempoAccountFallback(t *testing.T) {
	loc := berlin(t)
	c := tempoCfg{Account: "STD", PlaneMode: "ticket", Issue: "SAM-1"}
	plane := tempoEntry{ProjectID: 1, AllowsPlane: true, Duration: 600, StartTime: "2026-10-06T10:00:00"}
	for _, tc := range []struct {
		e          tempoEntry
		acc, from  string
		hashAcc    string
		ownTicketK bool
	}{
		{tempoEntry{TaskID: 1, JiraLink: "AB-1", TempoAccount: "B", ProjAccount: "P"}, "B", "Buchung", "B", true},
		{tempoEntry{TaskID: 1, JiraLink: "AB-1", ProjAccount: "P"}, "P", "Projekt", "P", true},
		{tempoEntry{TaskID: 1, JiraLink: "AB-1"}, "", "Ticket", "STD", true}, // eigenes Ticket: Tempo nimmt Ticket-Account, Hash mit Standard
		{plane, "STD", "Standard", "STD", false},
		{tempoEntry{TaskID: 1, JiraLink: "AB-1", TempoAccount: "  "}, "", "Buchung", "STD", true}, // nur Leerzeichen → leer (wie JS)
	} {
		tc.e.Duration, tc.e.StartTime = 600, "2026-10-06T10:00:00"
		p := tempoPlanOf(tc.e, c, tempoLookup{}, loc)
		if p.Account != tc.acc || p.AccountFrom != tc.from {
			t.Errorf("%+v: Account %q/%q, want %q/%q", tc.e, p.Account, p.AccountFrom, tc.acc, tc.from)
		}
		if want := tempoHash(p.Target, p.Started, p.Secs, p.Comment, tc.hashAcc); p.Hash != want {
			t.Errorf("%+v: Hash ohne Rückwärtskompatibilität", tc.e)
		}
		b := tempoBody(c, p)
		if _, has := b["attributes"]; has != (tc.acc != "") {
			t.Errorf("%+v: attributes nur mit Account", tc.e)
		}
	}
	b := tempoBody(tempoCfg{WorkerKey: "JIRAUSER1"}, tempoPlan{Target: "AB-1", Started: "s", Secs: 60, Comment: "c", Account: "X"})
	attr := b["attributes"].(map[string]any)["_Account_"].(map[string]any)
	if b["worker"] != "JIRAUSER1" || b["originTaskId"] != "AB-1" || attr["workAttributeId"] != 2 || attr["value"] != "X" || attr["name"] != "Account" {
		t.Errorf("Body: %#v", b)
	}
}

func TestTempoHashStable(t *testing.T) {
	if h := tempoHash("ABC-1", "2026-10-05T09:00:00.000", 3600, "Arbeit", ""); h != "f4212e5dea813327" { // Node-Wert
		t.Errorf("Hash %s", h)
	}
	// U+2028 bleibt in modernem JSON.stringify unescaped → Hash wie Node (nicht mehr \\u2028)
	if h := tempoHash("ZEIT-12", "2026-10-31T23:59:00.000", 60, "[PRJA-7] Tür \"x\" – ü\\ <a>&\u2028\t", "IMPROVEMENTS"); h != "2f9cab7700db017a" {
		t.Errorf("Hash mit Sonderzeichen %s", h)
	}
	a := tempoHash("A-1", "s", 60, "c", "")
	for _, b := range []string{tempoHash("A-2", "s", 60, "c", ""), tempoHash("A-1", "s", 120, "c", ""), tempoHash("A-1", "s", 60, "d", ""), tempoHash("A-1", "s", 60, "c", "X"), tempoHash("A-1", "t", 60, "c", "")} {
		if a == b {
			t.Error("Hash muss sich bei jeder Komponente ändern")
		}
	}
	if a != tempoHash("A-1", "s", 60, "c", "") || len(a) != 16 {
		t.Error("Hash nicht stabil")
	}
}

func TestTempoStatusOrder(t *testing.T) {
	p := tempoPlan{Target: "AB-1", Hash: "h"}
	ok := tempoEntry{AllowsTempo: true}
	for _, tc := range []struct {
		name   string
		e      tempoEntry
		p      tempoPlan
		locked bool
		want   string
	}{
		{"skip vor allem", tempoEntry{Skip: true, PushID: "1", PushHash: "h"}, p, true, "skipped"},
		{"norule", tempoEntry{}, p, false, "norule"},
		{"norule nur ungepusht", tempoEntry{PushID: "1", PushHash: "h"}, p, false, "synced"},
		{"review", tempoEntry{AllowsTempo: true, CheckFlags: "rest"}, p, false, "review"},
		{"review bestätigt", tempoEntry{AllowsTempo: true, CheckFlags: "rest", CheckNote: "ok"}, p, false, "new"},
		{"synced vor locked", tempoEntry{AllowsTempo: true, PushID: "1", PushHash: "h"}, p, true, "synced"},
		{"locked Monat", ok, p, true, "locked"},
		{"locked Fehlertext", tempoEntry{AllowsTempo: true, PushErr: "Tempo HTTP 400: Period is closed"}, p, false, "locked"},
		{"Sperrwort ohne 4xx", tempoEntry{AllowsTempo: true, PushErr: "Tempo HTTP 500: locked"}, p, false, "error"},
		{"dup", tempoEntry{AllowsTempo: true, Dup: "AB-2 09:00–10:00"}, p, false, "dup"},
		{"dup ok", tempoEntry{AllowsTempo: true, Dup: "ok"}, p, false, "new"},
		{"needs", ok, tempoPlan{Hash: "h"}, false, "needs"},
		{"needs NEU (Stufe 1)", ok, tempoPlan{Target: "NEU:ZEIT", Hash: "h"}, false, "needs"},
		{"error", tempoEntry{AllowsTempo: true, PushErr: "Tempo HTTP 502"}, p, false, "error"},
		{"changed", tempoEntry{AllowsTempo: true, PushID: "1", PushHash: "alt"}, p, false, "changed"},
		{"Anspruch läuft", tempoEntry{PushID: "pending:1"}, p, false, "new"},
	} {
		if got := tempoStatusOf(tc.e, tc.p, tc.locked); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestTempoLockAndAbort(t *testing.T) {
	for _, tc := range []struct {
		status      int
		text        string
		lock, abort bool
	}{
		{400, "Der Zeitraum ist gesperrt", true, false},
		{400, "Periode abgeschlossen", true, false},
		{403, "Period is locked", true, false},
		{400, "Timesheet already approved", true, false},
		{400, "Stundenzettel eingereicht", true, false},
		{400, "Worklog submitted for approval", true, false},
		{403, "Genehmigt – keine Änderung", true, false},
		{400, "Period frozen", true, false},
		{403, "You do not have permission", false, true},
		{401, "", false, true},
		{0, "Zeitüberschreitung", false, true},
		{500, "locked", false, false},
		{400, "Issue does not exist", false, false},
	} {
		if got := tempoLockError(tc.status, tc.text); got != tc.lock {
			t.Errorf("lock(%d,%q) = %v", tc.status, tc.text, got)
		}
		if got := tempoAbort(tc.status, tc.text); got != tc.abort {
			t.Errorf("abort(%d,%q) = %v", tc.status, tc.text, got)
		}
	}
	for in, want := range map[string]string{"APPROVED": "Stundenzettel genehmigt", "waiting_for_approval": "Stundenzettel eingereicht", "submitted": "Stundenzettel eingereicht", "open": "", "": ""} {
		l, r := approvalLock(in)
		if r != want || l != (want != "") {
			t.Errorf("approvalLock(%q) = %v %q", in, l, r)
		}
	}
}

func TestTempoDup(t *testing.T) {
	loc := berlin(t)
	at := func(s string) time.Time { x, _ := parseTimeIn(s, loc); return x }
	list := []map[string]any{
		{"tempoWorklogId": float64(11), "started": "2026-10-06 09:00:00.000", "timeSpentSeconds": float64(3600), "issue": map[string]any{"key": "AB-1"}, "comment": "Hand gebucht"},
		{"id": float64(12), "started": "2026-10-06T12:00:00.000+0000", "timeSpentSeconds": float64(1800), "originTaskId": "AB-2"},
		{"id": float64(13), "started": "2026-10-06 15:00:00.000", "timeSpentSeconds": float64(0)},    // Länge 0: nie Treffer
		{"id": float64(99), "started": "2026-10-06 09:00:00.000", "timeSpentSeconds": float64(3600)}, // eigener Worklog
		{"id": float64(14), "started": "kaputt", "timeSpentSeconds": float64(60)},
	}
	others := remoteWorklogs(list, map[string]bool{"99": true}, loc)
	if len(others) != 3 {
		t.Fatalf("fremde Worklogs: %d", len(others))
	}
	for _, tc := range []struct {
		s, e string
		want string
	}{
		{"2026-10-06T09:30:00", "2026-10-06T10:30:00", "AB-1"}, // 30 min von 60 = 50 %
		{"2026-10-06T09:31:00", "2026-10-06T10:31:00", ""},     // 29 min < 50 %
		{"2026-10-06T09:50:00", "2026-10-06T09:55:00", "AB-1"}, // kurze Buchung ganz drin
		{"2026-10-06T14:00:00", "2026-10-06T14:20:00", "AB-2"}, // +0000 = 14:00 Berlin
		{"2026-10-06T14:50:00", "2026-10-06T15:10:00", ""},     // Länge-0-Worklog um 15:00
		{"2026-10-06T10:00:00", "2026-10-06T11:00:00", ""},     // grenzt nur an
	} {
		hit := dupHit(at(tc.s), at(tc.e), others)
		got := ""
		if hit != nil {
			got = hit.Key
		}
		if got != tc.want {
			t.Errorf("dupHit(%s–%s) = %q, want %q", tc.s, tc.e, got, tc.want)
		}
	}
	if l := dupLabel(&others[0], loc); l != "AB-1 09:00–10:00 Hand gebucht" {
		t.Errorf("Label %q", l)
	}
}

func TestTempoHelpers(t *testing.T) {
	if worklogIDOf([]any{map[string]any{"tempoWorklogId": float64(5), "id": float64(6)}}) != "5" || worklogIDOf(map[string]any{"worklogId": "7"}) != "7" || worklogIDOf([]any{}) != "" {
		t.Error("worklogIDOf")
	}
	if f, l, ok := monthBounds("2028-02"); !ok || f != "2028-02-01" || l != "2028-02-29" {
		t.Errorf("monthBounds %s %s", f, l)
	}
	if leadingInt("12abc") != 12 || leadingInt("x") != 0 || leadingInt("") != 0 {
		t.Error("leadingInt")
	}
	if jsCollapse("a \u00a0\t\nb\ufeffc") != "a b c" || jsTrim("\ufeff x \u3000") != "x" {
		t.Error("JS-Leerraum")
	}
	if jsJSONString("a\"\\\b\f\n\r\t\x01ü<>&") != `"a\"\\\b\f\n\r\t\u0001ü<>&"` {
		t.Errorf("jsJSONString %s", jsJSONString("a\"\\\b\f\n\r\t\x01ü<>&"))
	}
}

func TestTempoRoutesAndAdminKeys(t *testing.T) {
	mux := http.NewServeMux()
	(&App{}).Routes(mux)
	for _, p := range [][2]string{{"GET", "/api/integrations/tempo-push/status"}, {"GET", "/api/integrations/tempo-push/outbox"},
		{"GET", "/api/integrations/tempo-push/settings"}, {"POST", "/api/integrations/tempo-push/push"}, {"POST", "/api/integrations/tempo-push/dup"},
		{"DELETE", "/api/integrations/tempo-push/months/2026-10"}, {"DELETE", "/api/integrations/tempo-push/deletions/3"},
		{"GET", "/api/admin/jira-instances"}, {"POST", "/api/admin/jira-instances"}, {"PUT", "/api/admin/jira-instances/2"}, {"DELETE", "/api/admin/jira-instances/2"},
		{"GET", "/api/jira/instances"}, {"GET", "/api/jira/instances/2/settings"}, {"PUT", "/api/jira/instances/2/settings"},
		{"POST", "/api/integrations/tempo-push/suggest"}, {"GET", "/api/jira/status"}, {"GET", "/api/jira/search"}, {"GET", "/api/jira/projects"},
		{"GET", "/api/integrations/jira/my-tickets/status"}, {"GET", "/api/integrations/tempo/status"}, {"GET", "/api/integrations/plane/map"}, {"GET", "/api/integrations/plane/my-items"},
		{"POST", "/api/integrations/plane/import"}, {"GET", "/api/jira/issue/AB-1"}, {"POST", "/api/jira/issue/AB-1/comment"}, {"POST", "/api/jira/issue/AB-1/transition"},
		{"POST", "/api/jira/issue/AB-1/task"}, {"POST", "/api/jira/issues"}, {"PUT", "/api/jira/projects"}, {"POST", "/api/integrations/jira/my-tickets/sync"},
		{"GET", "/api/integrations/tempo-push/accounts"}, {"POST", "/api/integrations/tempo-push/entry-account"}, {"POST", "/api/integrations/tempo-push/skip"},
		{"POST", "/api/integrations/tempo-push/check"}, {"PUT", "/api/integrations/tempo-push/project/3"}, {"GET", "/api/integrations/tempo-push/autosetup"},
		{"POST", "/api/integrations/tempo-push/autosetup"}, {"GET", "/api/integrations/plane/map/suggest"}, {"POST", "/api/integrations/plane/map/apply"}, {"POST", "/api/integrations/tempo-push/target"}, {"POST", "/api/integrations/tempo-push/ticket"}} {
		req, _ := http.NewRequest(p[0], p[1], nil)
		if _, pat := mux.Handler(req); pat == "" {
			t.Errorf("%s %s: keine Route", p[0], p[1])
		}
	}
	a := &App{}
	for in, ok := range map[string]bool{"0": true, "1": true, " 1 ": true, "2": false, "ja": false, "": false} {
		if _, err := a.checkAdminConfig("module_tempo", in); (err == nil) != ok {
			t.Errorf("module_tempo=%q: %v", in, err)
		}
	}
	if _, err := a.checkAdminConfig("jira_base_url", "http://jira.extern"); err == nil {
		t.Error("jira_base_url ohne https muss abgelehnt werden")
	}
}

func TestResolveTempoInstance(t *testing.T) {
	insts := []instKeys{{ID: 1, Name: "Jira A", Keys: map[string]bool{"AB": true, "OPS": true}, NewProject: "ZEIT", Usable: true},
		{ID: 2, Name: "Jira B", Keys: map[string]bool{"XY": true, "OPS": true}, NewProject: "ORGA", Usable: true}}
	for _, tc := range []struct {
		target, kind string
		month        int64
		want         int64
		problem      string
	}{
		{"AB-1", "ticket", 0, 1, ""},
		{"XY-7", "collector", 0, 2, ""},
		{"OPS-3", "ticket", 0, 0, "mehreren Jira-Instanzen (Jira A, Jira B)"}, // mehrdeutig: nie raten
		{"ZZ-1", "ticket", 0, 0, "keiner Jira-Instanz bekannt"},
		{"ZEIT-9", "month", 2, 2, ""}, // Übergangs-Ticket: Instanz aus tempo_month_tickets
		{"ZEIT-9", "month", 0, 0, "keiner Jira-Instanz zugeordnet"},
		{"", "no-ticket", 0, 0, ""},
		{"NEU:ZEIT", "create", 0, 0, ""},
		{"NEU:ZEIT Monat 2026-10", "create-month", 0, 1, ""}, // Übergangs-Ticket: Instanz mit diesem Projekt für neue Tickets
		{"NEU:HR Monat 2026-10", "create-month", 0, 0, "nicht eindeutig"},
	} {
		id, p := resolveTempoInstance(tc.target, tc.kind, tc.month, insts)
		if id != tc.want || (tc.problem == "") != (p == "") || !strings.Contains(p, tc.problem) {
			t.Errorf("%s/%s: %d %q, want %d %q", tc.target, tc.kind, id, p, tc.want, tc.problem)
		}
	}
}

func TestTempoItemStatus(t *testing.T) {
	a := &tempoInst{jiraInstance: jiraInstance{ID: 1}}
	b := &tempoInst{jiraInstance: jiraInstance{ID: 2}}
	p := tempoPlan{Target: "AB-1", Hash: "h"}
	base := tempoEntry{AllowsTempo: true, Date: "2026-10-06"}
	pushed := func(inst int64, hash string) tempoEntry {
		e := base
		e.PushID, e.PushHash, e.PushInstance = "9", hash, inst
		return e
	}
	for _, tc := range []struct {
		name string
		it   tempoItem
		lock map[int64]map[string]bool
		want string
	}{
		{"synced gleiche Instanz", tempoItem{E: pushed(1, "h"), P: p, Inst: a}, nil, "synced"},
		{"Instanz gewechselt", tempoItem{E: pushed(1, "h"), P: p, Inst: b}, nil, "needs"},
		{"Instanz gewechselt, neue nicht nutzbar", tempoItem{E: pushed(1, "h"), P: p, Inst: b, Problem: "kein Token"}, nil, "needs"},
		{"synced, Zuordnung jetzt unklar", tempoItem{E: pushed(1, "h"), P: p, Problem: "mehrdeutig"}, nil, "synced"},
		{"Worklog ohne Instanz", tempoItem{E: pushed(0, "h"), P: p, Inst: a, Problem: "gelöscht"}, nil, "needs"},
		{"geändert, Instanz unklar", tempoItem{E: pushed(1, "alt"), P: p, Problem: "mehrdeutig"}, nil, "needs"},
		{"neu, mehrdeutig", tempoItem{E: base, P: p, Problem: "mehrdeutig"}, nil, "needs"},
		{"neu", tempoItem{E: base, P: p, Inst: a}, nil, "new"},
		{"Sperre nur in eigener Instanz", tempoItem{E: base, P: p, Inst: a}, map[int64]map[string]bool{2: {"2026-10": true}}, "new"},
		{"gesperrt", tempoItem{E: base, P: p, Inst: a}, map[int64]map[string]bool{1: {"2026-10": true}}, "locked"},
	} {
		if got := tempoItemStatus(tc.it, tc.lock); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestJiraTokenBinding(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	inst := jiraInstance{ID: 1, BaseURL: "https://jira-a.firma.de"}
	blob, err := crypto.SealAAD(key, []byte("pat"), jiraTokenAAD(7, inst.ID, inst.BaseURL))
	if err != nil {
		t.Fatal(err)
	}
	if p, err := openJiraToken(key, blob, 7, inst); err != nil || string(p) != "pat" {
		t.Fatalf("eigenes Token: %v", err)
	}
	other := []struct {
		name string
		uid  int64
		inst jiraInstance
	}{
		{"andere Person", 8, inst},
		{"andere Instanz", 7, jiraInstance{ID: 2, BaseURL: inst.BaseURL}},
		{"anderer Host", 7, jiraInstance{ID: 1, BaseURL: "https://jira-b.firma.de"}},
		{"http statt https", 7, jiraInstance{ID: 1, BaseURL: "http://jira-a.firma.de"}},
	}
	for _, o := range other {
		if _, err := openJiraToken(key, blob, o.uid, o.inst); err == nil {
			t.Errorf("%s: Blob darf sich nicht öffnen lassen", o.name)
		}
	}
	if p, err := openJiraToken(key, blob, 7, jiraInstance{ID: 1, BaseURL: "https://JIRA-A.firma.de/jira"}); err != nil || string(p) != "pat" {
		t.Error("gleicher Host mit Pfad/Großschreibung bleibt lesbar")
	}
	legacy, _ := crypto.Seal(key, []byte("pat"))
	if _, err := openJiraToken(key, legacy, 7, inst); err == nil {
		t.Error("altes Format ohne Bindung darf zur Laufzeit nicht gelten")
	}
}

func TestCheckInstance(t *testing.T) {
	a := &App{}
	ok := jiraInstance{Name: " Jira A ", Kind: "dc", Tempo: "dc", BaseURL: "https://jira.firma.de/", Enabled: true}
	if got, err := a.checkInstance(ok); err != nil || got.Name != "Jira A" || got.BaseURL != "https://jira.firma.de" {
		t.Fatalf("gültig: %+v %v", got, err)
	}
	for name, f := range map[string]func(*jiraInstance){
		"Name leer":           func(i *jiraInstance) { i.Name = "  " },
		"Name zu lang":        func(i *jiraInstance) { i.Name = strings.Repeat("x", 61) },
		"Art":                 func(i *jiraInstance) { i.Kind = "server" },
		"Tempo":               func(i *jiraInstance) { i.Tempo = "x" },
		"http":                func(i *jiraInstance) { i.BaseURL = "http://jira.firma.de" },
		"leer":                func(i *jiraInstance) { i.BaseURL = "" },
		"Cloud einschalten":   func(i *jiraInstance) { i.Kind = "cloud" },
		"Tempo Cloud ein":     func(i *jiraInstance) { i.Tempo = "cloud" },
		"Zugangsdaten in URL": func(i *jiraInstance) { i.BaseURL = "https://u:p@jira.firma.de" },
	} {
		x := ok
		f(&x)
		if _, err := a.checkInstance(x); err == nil {
			t.Errorf("%s: müsste abgelehnt werden", name)
		}
	}
	cloud := ok
	cloud.Kind, cloud.Enabled = "cloud", false
	if _, err := a.checkInstance(cloud); err != nil {
		t.Errorf("Cloud ausgeschaltet anlegbar: %v", err)
	}
	if n := len([]rune(strings.Repeat("ä", 60))); n != 60 {
		t.Fatal(n)
	}
	x := ok
	x.Name = strings.Repeat("ä", 60) // 60 Zeichen, mehr Bytes
	if _, err := a.checkInstance(x); err != nil {
		t.Errorf("60 Umlaute erlaubt: %v", err)
	}
}

// recTx zeichnet Exec-Aufrufe auf (nur Exec wird benutzt)
type recTx struct {
	pgx.Tx
	sql  []string
	args [][]any
}

func (r *recTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	r.sql, r.args = append(r.sql, sql), append(r.args, args)
	return pgconn.NewCommandTag("DELETE 3"), nil
}

func TestDropInstanceTokensScope(t *testing.T) {
	tx := &recTx{}
	n, err := (&App{}).dropInstanceTokens(context.Background(), tx, 5)
	if err != nil || n != 3 || len(tx.sql) != 3 {
		t.Fatalf("%d %v %v", n, err, tx.sql)
	}
	if !strings.Contains(tx.sql[0], "kind='jira_token' AND scope=$1") || tx.args[0][0] != int64(5) {
		t.Errorf("Tokens nur dieser Instanz: %s %v", tx.sql[0], tx.args[0])
	}
	keys := tx.args[1][0].([]string)
	for _, k := range keys {
		if !strings.HasPrefix(k, "i5:") {
			t.Errorf("fremder Schlüssel %q", k)
		}
	}
	if !strings.Contains(tx.sql[2], "instance_id=$1") || tx.args[2][0] != int64(5) {
		t.Errorf("Sperren nur dieser Instanz: %s", tx.sql[2])
	}
}

func TestTempoAssignStaysInInstance(t *testing.T) {
	loc := berlin(t)
	mk := func(id int64, name string, keys ...string) *tempoInst {
		k := map[string]bool{}
		for _, x := range keys {
			k[x] = true
		}
		return &tempoInst{jiraInstance: jiraInstance{ID: id, Name: name, Enabled: true}, C: tempoCfg{JiraKeys: k, Since: "2026-10-01", Account: name + "-ACC", AccountAttrID: 2}}
	}
	a, b := mk(1, "A", "AB"), mk(2, "B", "XY", "AB")
	s := &tempoSetup{Person: tempoCfg{PlaneMode: "month", Since: "2026-10-01", JiraKeys: map[string]bool{"AB": true, "XY": true}}, Insts: []*tempoInst{a, b}, byID: map[int64]*tempoInst{1: a, 2: b}}
	keys := s.keys()
	base := tempoEntry{TaskID: 1, TicketSys: "jira", Date: "2026-10-06", Duration: 600, StartTime: "2026-10-06T10:00:00", AllowsTempo: true}
	e := base
	e.Ticket = "AB-1" // AB in A und B → mehrdeutig
	if it, _ := s.assign(e, tempoLookup{}, keys, loc); it.Inst != nil || !strings.Contains(it.Problem, "mehreren") || tempoItemStatus(it, nil) != "needs" {
		t.Errorf("mehrdeutig: %+v", it)
	}
	e.PushID, e.PushInstance, e.PushHash = "9", 1, "x" // schon in A übertragen → bleibt in A, trotz Mehrdeutigkeit
	if it, _ := s.assign(e, tempoLookup{}, keys, loc); it.Inst != a || it.Problem != "" || it.P.Account != "" {
		t.Errorf("bleibt in A: %+v", it)
	}
	e2 := base
	e2.Ticket, e2.PushID, e2.PushInstance = "XY-5", "9", 1 // Ticket passt nur noch zu B → nicht umziehen
	if it, _ := s.assign(e2, tempoLookup{}, keys, loc); it.Inst != a || !strings.Contains(it.Problem, "nicht umgezogen") || tempoItemStatus(it, nil) != "needs" {
		t.Errorf("kein Umzug: %+v", it)
	}
	e3 := base
	e3.Ticket = "XY-5"
	if it, _ := s.assign(e3, tempoLookup{}, keys, loc); it.Inst != b || it.Problem != "" || tempoItemStatus(it, nil) != "new" {
		t.Errorf("eindeutig B: %+v", it)
	}
	e4 := e3
	e4.PushID, e4.PushInstance = "9", 0 // Instanz gelöscht
	if it, _ := s.assign(e4, tempoLookup{}, keys, loc); !strings.Contains(it.Problem, "gelöschten") || tempoItemStatus(it, nil) != "needs" {
		t.Errorf("verwaist: %+v", it)
	}
	b.C.Since = "2026-11-01"
	if _, ok := s.assign(e3, tempoLookup{}, keys, loc); ok {
		t.Error("vor der Grenze der Instanz: nicht nach Tempo")
	}
}
