package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestJiraSearchJQL(t *testing.T) {
	// Erwartung aus backend/jira.js GET /api/jira/search
	for _, tc := range []struct{ q, jql, mine, open, want string }{
		{"", "", "", "", "assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC"},
		{"abc-12", "", "", "1", `key = "ABC-12" AND resolution = Unresolved ORDER BY updated DESC`},
		{`Firewall "neu"`, "", "1", "0", `text ~ "Firewall  neu " AND assignee = currentUser() ORDER BY updated DESC`},
		{"x", " project = ZEIT ", "", "", "project = ZEIT"},
	} {
		if got := jiraSearchJQL(tc.q, tc.jql, tc.mine, tc.open); got != tc.want {
			t.Errorf("%+v: %q", tc, got)
		}
	}
}

func TestShapeIssue(t *testing.T) {
	var i map[string]any
	_ = json.Unmarshal([]byte(`{"key":"ABC-1","fields":{"summary":"Login","status":{"name":"In Arbeit","statusCategory":{"key":"indeterminate"}},
		"priority":{"name":"High"},"issuetype":{"name":"Task"},"project":{"key":"ABC","name":"Alpha"},"assignee":{"displayName":"C. Mustermann"},
		"updated":"2026-11-08T10:00:00.000+0100","duedate":null}}`), &i)
	got := shapeIssue(i, map[string]*bookedSum{"ABC-1": {Secs: 3600, TaskID: 7}})
	want := map[string]any{"key": "ABC-1", "summary": "Login", "status": "In Arbeit", "status_cat": "indeterminate", "priority": "High", "type": "Task",
		"project": "ABC", "project_name": "Alpha", "assignee": "C. Mustermann", "updated": "2026-11-08T10:00:00.000+0100", "due": nil, "booked_seconds": int64(3600), "task_id": int64(7)}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %v, want %v", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("Felder: %v", got)
	}
	empty := shapeIssue(map[string]any{"key": "X-1", "fields": map[string]any{}}, nil)
	if empty["booked_seconds"] != int64(0) || empty["task_id"] != nil || empty["assignee"] != nil {
		t.Errorf("leer: %v", empty)
	}
}

func TestPickInstancesAndCacheKey(t *testing.T) {
	a := &tempoInst{jiraInstance: jiraInstance{ID: 1, Name: "A", Enabled: true}}
	b := &tempoInst{jiraInstance: jiraInstance{ID: 2, Name: "B", Enabled: true}, Problem: "Kein Jira-Token für „B“ (Profil)"}
	c := &tempoInst{jiraInstance: jiraInstance{ID: 3, Name: "C", Enabled: false}, Problem: "aus"}
	s := &tempoSetup{Insts: []*tempoInst{a, b, c}, byID: map[int64]*tempoInst{1: a, 2: b, 3: c}}
	if l, why := pickInstances(s, ""); len(l) != 1 || l[0] != a || why != "" {
		t.Errorf("nur nutzbare: %v %q", l, why)
	}
	if l, why := pickInstances(s, "2"); l != nil || !strings.Contains(why, "Token") {
		t.Errorf("gewählt, nicht nutzbar: %q", why)
	}
	if l, _ := pickInstances(s, "3"); l != nil {
		t.Error("ausgeschaltete Instanz nie")
	}
	if l, _ := pickInstances(s, "x"); l != nil {
		t.Error("ungültige Angabe")
	}
	if searchCacheKey(1, 1, 0, "q") == searchCacheKey(2, 1, 0, "q") || searchCacheKey(1, 1, 0, "q") == searchCacheKey(1, 2, 0, "q") {
		t.Error("Cache-Schlüssel muss Person und Instanz enthalten")
	}
}

func TestMyItemsFilterAndMapRows(t *testing.T) {
	var list []map[string]any
	_ = json.Unmarshal([]byte(`[{"id":"w1","sequence_id":7,"name":"Login","state":"s1","assignees":["me"],"updated_at":"2026-11-08"},
		{"id":"w2","sequence_id":8,"name":"Fertig","state":{"id":"s2"},"assignees":[{"id":"me"}]},
		{"id":"w3","sequence_id":9,"name":"Fremd","state":"s1","assignees":["other"]}]`), &list)
	states := map[string][2]string{"s1": {"started", "In Arbeit"}, "s2": {"completed", "Erledigt"}}
	got := myItemsFilter(list, states, "me", localProj{ID: 4, PlaneProject: "pp", Ident: "prja"})
	if len(got) != 1 || got[0]["key"] != "PRJA-7" || got[0]["state"] != "In Arbeit" || got[0]["local_project_id"] != int64(4) || got[0]["mine"] != true {
		t.Errorf("meine Work-Items: %v", got)
	}
	rows := []map[string]any{{"jira_key": "ABC-1", "title": "ABC-1", "entries": int64(2), "secs": int64(5430)}, {"jira_key": "XY-2", "title": "Doku", "secs": int64(7200)}, {"jira_key": "kaputt"}}
	m := mapRowsShape(rows, map[string]string{"XY-2": "skipped"})
	if len(m) != 2 || m[0]["jira_key"] != "XY-2" || m[0]["status"] != "skipped" || m[1]["title"] != nil || m[1]["minutes"] != int64(91) || m[1]["status"] != "open" {
		t.Errorf("Zuordnung: %v", m)
	}
}

func TestTempoConnCheck(t *testing.T) {
	for _, tc := range []struct {
		tempo int
		want  string
		conn  bool
	}{{200, "Verbindung OK ✓ – eingeloggt als C. Mustermann (harm)", true}, {401, "Jira OK (harm), aber Tempo-Zugriff verweigert (401) – Tempo-Lizenz prüfen", false}, {500, "Jira OK (harm), Tempo: HTTP 500", false}} {
		a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/rest/api/2/myself" {
				_, _ = io.WriteString(w, `{"name":"harm","key":"JIRAUSER1","displayName":"C. Mustermann"}`)
				return
			}
			w.WriteHeader(tc.tempo)
			_, _ = io.WriteString(w, `[]`)
		})
		a.Loc = berlin(t)
		ti := &tempoInst{jiraInstance: jiraInstance{ID: 1}, C: tempoCfg{Base: c.Base, Token: c.Token}}
		res := a.tempoConnCheck(context.Background(), 1, ti)
		if res["message"] != tc.want || res["connected"] != tc.conn || res["configured"] != true {
			t.Errorf("%d: %v", tc.tempo, res)
		}
		if b, _ := json.Marshal(res); strings.Contains(string(b), testToken) {
			t.Error("Token in der Antwort")
		}
	}
}

func TestInstSums(t *testing.T) {
	all := map[string]*bookedSum{"ABC-1": {Secs: 60, TaskID: 1}, "2|ABC-1": {Secs: 120, TaskID: 2}, "3|ABC-1": {Secs: 900, TaskID: 3}}
	if s := instSums(all, 2)["ABC-1"]; s == nil || s.Secs != 180 {
		t.Errorf("Instanz 2: %+v", s)
	}
	if s := instSums(all, 9)["ABC-1"]; s == nil || s.Secs != 60 {
		t.Errorf("fremde Instanz zählt nicht: %+v", s)
	}
	if all["ABC-1"].Secs != 60 {
		t.Error("Eingabe darf nicht verändert werden")
	}
}
