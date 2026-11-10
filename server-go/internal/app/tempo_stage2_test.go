package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMonthDescriptionNodeCompat(t *testing.T) {
	rows := []monthRow{
		{Ticket: "prja-7", Title: "Login | [neu]", Project: "Proj {A}", Secs: 3600, Date: "2026-10-02"},
		{Ticket: "PRJA-7", Title: "Login | [neu]", Project: "Proj {A}", Secs: 1800, Date: "2026-10-05"},
		{Secs: 7290, Date: "2026-10-06"},
		{Ticket: "PRJA-9", Title: "Doku", Secs: 900, Date: "2026-10-07"},
	}
	// erzeugt mit dem Code aus backend/tempo-push.js updateMonthDescription
	want := "Übergangs-Ticket für Zeiten von harm im Monat 2026-10, die in Plane gebucht wurden. Wird von Zeitprotokoll aktuell gehalten – bitte nicht von Hand ändern.\n\n*Summe: 3,8 h in 4 Buchungen*\n\n||Plane-Ticket||Titel||Projekt||Stunden||Buchungen||zuletzt||\n|(ohne Ticket)| | |2|1|2026-10-06|\n|[PRJA-7|https://plane.x/hon/browse/PRJA-7/]|Login    neu|Proj  A|1,5|2|2026-10-05|\n|[PRJA-9|https://plane.x/hon/browse/PRJA-9/]|Doku| |0,3|1|2026-10-07|"
	got := monthDescription("harm", "2026-10", "https://plane.x/hon", rows)
	if got != want {
		t.Errorf("Beschreibung:\n%q\n%q", got, want)
	}
	if descHash(got) != descHash(monthDescription("harm", "2026-10", "https://plane.x/hon", rows)) {
		t.Error("Hash stabil")
	}
	rows[0].Secs += 3600
	if descHash(got) == descHash(monthDescription("harm", "2026-10", "https://plane.x/hon", rows)) {
		t.Error("geänderte Zeit muss die Beschreibung ändern")
	}
	if monthTicketSummary("user01", "2026-10") != "Übergang Zeiten user01 2026-10" {
		t.Error("Überschrift wie Node")
	}
}

func TestMonthWantClosed(t *testing.T) {
	for _, tc := range []struct {
		approval                     string
		tlocked, grace, open, closed bool
		want                         bool
	}{
		{"approved", false, false, true, false, true},    // Freigabe schließt immer
		{"rejected", true, true, false, true, false},     // wiedereröffnet/abgelehnt: öffnen (Nachträge)
		{"submitted", true, true, false, true, true},     // eingereicht: kein Auf/Zu (kein Flip-Flop)
		{"submitted", false, false, false, false, false}, // nach Ablehnung neu eingereicht: bleibt offen
		{"", true, false, true, false, true},             // Tempo-Sperre
		{"", false, true, false, false, true},            // Nachfrist vorbei, nichts offen
		{"", false, true, true, false, false},            // Nachfrist vorbei, aber Offenes
		{"", false, false, false, false, false},
	} {
		if got := monthWantClosed(tc.approval, tc.tlocked, tc.grace, tc.open, tc.closed); got != tc.want {
			t.Errorf("%+v: %v", tc, got)
		}
	}
	for _, tc := range []struct {
		month string
		days  int
		today string
		want  bool
	}{{"2026-10", 5, "2026-11-05", false}, {"2026-10", 5, "2026-11-06", true}, {"2026-02", 0, "2026-03-01", true}, {"2026-02", 0, "2026-02-28", false}} {
		if got := monthGraceOver(tc.month, tc.days, tc.today); got != tc.want {
			t.Errorf("Nachfrist %+v: %v", tc, got)
		}
	}
}

func TestPickTransition(t *testing.T) {
	tr := func(id, name, to, cat string) map[string]any {
		return map[string]any{"id": id, "name": name, "to": map[string]any{"name": to, "statusCategory": map[string]any{"key": cat}}}
	}
	list := []map[string]any{tr("11", "In Arbeit", "In Arbeit", "indeterminate"), tr("31", "Erledigen", "Erledigt", "done"), tr("41", "Wieder öffnen", "Offen", "new")}
	if id := pickTransition(list, true); id != "31" {
		t.Errorf("schließen: %s", id)
	}
	if id := pickTransition(list, false); id != "11" && id != "41" {
		t.Errorf("wiederöffnen: %s", id)
	}
	if id := pickTransition([]map[string]any{tr("5", "Close", "Closed", "")}, true); id != "5" {
		t.Errorf("über den Namen: %s", id)
	}
	if id := pickTransition([]map[string]any{tr("5", "Close", "Closed", "done")}, false); id != "" {
		t.Errorf("kein Wiederöffnen in Erledigt: %s", id)
	}
	if id := pickTransition(nil, true); id != "" {
		t.Error("leer")
	}
}

func TestExplicitInstanceAndRequestKey(t *testing.T) {
	e := tempoEntry{TempoTarget: " ab-1 ", TargetInst: 3, JiraLink: "XY-2", TaskInst: 4, ProjIssue: "SAM-1", ProjIssueInst: 5}
	for target, want := range map[string]int64{"AB-1": 3, "XY-2": 4, "SAM-1": 5, "ZZ-9": 0, "": 0} {
		if got := explicitInstance(e, target); got != want {
			t.Errorf("%s: %d", target, got)
		}
	}
	a, b := requestKeyOf("", 1, []int64{3, 1, 2}, "Titel"), requestKeyOf("", 1, []int64{1, 2, 3}, " Titel ")
	if a != b || !strings.HasPrefix(a, "auto:") {
		t.Errorf("Schlüssel unabhängig von Reihenfolge/Leerraum: %s %s", a, b)
	}
	if a == requestKeyOf("", 2, []int64{1, 2, 3}, "Titel") || a == requestKeyOf("", 1, []int64{1, 2}, "Titel") {
		t.Error("andere Instanz/Buchungen → anderer Auftrag")
	}
	if requestKeyOf("klick-42", 1, nil, "") != "klick-42" {
		t.Error("Schlüssel des Clients gilt")
	}
}

func TestTicketScopeAndSuggest(t *testing.T) {
	same := []ticketEntry{{ID: 1, TaskID: 7}, {ID: 2, TaskID: 7}}
	mixed := []ticketEntry{{ID: 1, TaskID: 7}, {ID: 2}}
	if s, _ := ticketScope("", same); s != "task" {
		t.Error("gleiche Aufgabe → task")
	}
	if s, _ := ticketScope("", mixed); s != "entry" {
		t.Error("gemischt → entry")
	}
	if _, err := ticketScope("task", mixed); err == nil {
		t.Error("task erzwungen bei gemischt → Fehler")
	}
	today, _ := time.Parse("2006-01-02", "2026-11-09")
	corpus := []corpusItem{
		{Key: "AB-1", Title: "Firewall-Regeln anpassen", Projects: map[int64]int{4: 3}, Count: 3, Last: "2026-11-01"},
		{Key: "AB-2", Title: "Urlaubsplanung", Projects: map[int64]int{}, Count: 1, Last: "2025-01-01"},
	}
	got := suggestLocal("Firewallregeln anpassen Kunde", 4, corpus, today)
	if len(got) != 1 || got[0].Key != "AB-1" || got[0].Score < 60 {
		t.Fatalf("Vorschlag: %+v", got)
	}
	before := got[0].Score
	merged := mergeRemote(got, []suggestion{{Key: "AB-1", Score: 50, InstanceID: int64(1)}, {Key: "CD-3", Score: 40, InstanceID: int64(2)}})
	if len(merged) != 2 || merged[0].Key != "AB-1" || merged[0].Score != min(99, before+8) || merged[0].InstanceID != int64(1) {
		t.Errorf("zusammenführen: %+v", merged)
	}
}

func TestJiraCreateIssueRetries(t *testing.T) {
	var bodies []map[string]any
	a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/issue":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			f := b["fields"].(map[string]any)
			bodies = append(bodies, f)
			if _, has := f["assignee"]; has {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"errors":{"assignee":"User cannot be assigned"}}`)
				return
			}
			if v, has := f["customfield_10100"]; !has || v != "77" {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"errors":{"customfield_10100":"Account is required."}}`)
				return
			}
			_, _ = io.WriteString(w, `{"key":"ZEIT-501"}`)
		case "/rest/api/2/project/ZEIT":
			_, _ = io.WriteString(w, `{"id":"100"}`)
		case "/rest/tempo-accounts/1/link/project/100":
			_, _ = io.WriteString(w, `[{"accountId":77,"defaultAccount":true}]`)
		default:
			w.WriteHeader(404)
		}
	})
	key, err := a.jiraCreateIssue(context.Background(), 1, c, map[string]any{"project": map[string]any{"key": "ZEIT"}, "summary": "X", "issuetype": map[string]any{"name": "Task"}}, "harm", "")
	if err != nil || key != "ZEIT-501" {
		t.Fatalf("anlegen: %q %v", key, err)
	}
	// 1. mit Zuweisung (400), 2. ohne (Account fehlt), 3. Account als Zahl, 4. als Text → ok
	if len(bodies) != 4 || bodies[2]["customfield_10100"] != float64(77) || bodies[3]["customfield_10100"] != "77" {
		t.Errorf("Versuche: %d %v", len(bodies), bodies)
	}
}

func TestJiraFindSummaryAndTransitions(t *testing.T) {
	var posted string
	a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/2/search":
			jql := r.URL.Query().Get("jql")
			if !strings.Contains(jql, `project = "ZEIT"`) || !strings.Contains(jql, `summary ~ "\"Übergang Zeiten harm 2026-10\""`) {
				w.WriteHeader(400)
				return
			}
			_, _ = io.WriteString(w, `{"issues":[{"key":"ZEIT-1","fields":{"summary":"Übergang Zeiten harm 2026-10 (alt)"}},{"key":"ZEIT-2","fields":{"summary":" Übergang Zeiten harm 2026-10 "}}]}`)
		case r.URL.Path == "/rest/api/2/issue/ZEIT-2/transitions" && r.Method == "GET":
			_, _ = io.WriteString(w, `{"transitions":[{"id":"21","name":"Start","to":{"name":"In Arbeit","statusCategory":{"key":"indeterminate"}}},{"id":"31","name":"Fertig","to":{"name":"Erledigt","statusCategory":{"key":"done"}}}]}`)
		case r.URL.Path == "/rest/api/2/issue/ZEIT-2/transitions" && r.Method == "POST":
			b, _ := io.ReadAll(r.Body)
			posted = string(b)
			w.WriteHeader(204)
		case r.URL.Path == "/rest/api/2/issue/createmeta/ZEIT/issuetypes":
			_, _ = io.WriteString(w, `{"values":[{"name":"Sub","subtask":true},{"name":"Aufgabe"},{"name":"Bug"}]}`)
		default:
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	key, err := a.jiraFindSummary(ctx, 1, c, "ZEIT", "Übergang Zeiten harm 2026-10")
	if err != nil || key != "ZEIT-2" { // unscharfe JQL → exakt nachgeprüft
		t.Fatalf("wiederfinden: %q %v", key, err)
	}
	ti := &tempoInst{C: tempoCfg{Base: c.Base, Token: c.Token}}
	if err := a.monthTransition(ctx, 1, ti, "ZEIT-2", true); err != nil || !strings.Contains(posted, `"31"`) {
		t.Errorf("schließen: %v %s", err, posted)
	}
	if err := a.monthTransition(ctx, 1, ti, "ZEIT-2", false); err != nil || !strings.Contains(posted, `"21"`) {
		t.Errorf("wiederöffnen: %v %s", err, posted)
	}
	types, err := a.jiraIssueTypes(ctx, 1, c, "ZEIT")
	if err != nil || pickIssueType(types, "Task") != "Aufgabe" || pickIssueType(types, "bug") != "Bug" || pickIssueType(nil, "Task") != "Task" {
		t.Errorf("Vorgangstyp: %v %v", types, err)
	}
}

func TestTempoNeedsTicketStage2(t *testing.T) {
	if tempoNeedsTicket(tempoPlan{Target: "NEU:ZEIT Monat 2026-10", Kind: "create-month"}) {
		t.Error("Übergangs-Ticket legt der Push selbst an")
	}
	for _, k := range []string{"create", "create-collector"} {
		if !tempoNeedsTicket(tempoPlan{Target: "NEU:ZEIT", Kind: k}) {
			t.Errorf("%s: nur auf Bestätigung → needs", k)
		}
	}
}

func TestSuggestSwitches(t *testing.T) {
	for _, tc := range []struct {
		server  string
		blocked bool
		user    string
		on      bool
		why     string
	}{
		{"", false, "1", true, ""}, // Standard: an
		{"1", false, "", true, ""},
		{"0", false, "1", false, "Server"},        // Stufe 1: Server
		{"1", true, "1", false, "Administrator"},  // Stufe 2: Admin sperrt die Person
		{"1", false, "0", false, "Einstellungen"}, // Stufe 3: Person schaltet ab
		{"0", true, "0", false, "Server"},
	} {
		on, why := suggestOn(tc.server, tc.blocked, tc.user)
		if on != tc.on || !strings.Contains(why, tc.why) {
			t.Errorf("%+v: %v %q", tc, on, why)
		}
	}
	var hits int
	a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = io.WriteString(w, `{"issues":[{"key":"AB-1","fields":{"summary":"Firewall Regeln"}}]}`)
	})
	ti := &tempoInst{jiraInstance: jiraInstance{ID: 9, Name: "J"}, C: tempoCfg{Base: c.Base, Token: c.Token}}
	if l := a.remoteSuggestions(context.Background(), 1, false, []*tempoInst{ti}, "Firewall Regeln prüfen", nil); len(l) != 0 || hits != 0 {
		t.Fatalf("aus: kein Aufruf, keine Vorschläge (%d Aufrufe)", hits)
	}
	if l := a.remoteSuggestions(context.Background(), 1, true, []*tempoInst{ti}, "Firewall Regeln prüfen", nil); len(l) != 1 || hits != 1 || l[0].InstanceID != int64(9) {
		t.Errorf("an: %v (%d Aufrufe)", l, hits)
	}
}
