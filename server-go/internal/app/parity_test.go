package app

import (
	"encoding/json"
	"testing"
)

func TestMapScoreNodeCompat(t *testing.T) {
	// Node: similarity = Jaccard über Wörter ≥ 3 Zeichen; score = min(1, base + 0,6·sim + 0,4 wenn Key im Namen), 2 Stellen
	if s := nodeSim("Firewall Regeln anpassen", "Firewall Regeln prüfen"); s != 0.5 {
		t.Errorf("Ähnlichkeit %v", s)
	}
	for _, tc := range []struct {
		title, name string
		base, want  float64
	}{
		{"Firewall Regeln anpassen", "Firewall Regeln prüfen", 0, 0.3},
		{"", "ABC-1 Login", 0.2, 0.6},
		{"Login", "ABC-1 Login", 0.2, 0.9}, // Wörter abc, login → Ähnlichkeit 0,5
		{"x", "", 0, 0},
	} {
		if got := mapScore("ABC-1", tc.title, tc.name, tc.base); got != tc.want {
			t.Errorf("%+v: %v", tc, got)
		}
	}
	if mapTitle("ABC-1", "abc-1") != "ABC-1 (aus Jira übernommen)" || mapTitle("ABC-1", "Login") != "Login" {
		t.Error("Titel wie Node")
	}
}

func TestAutosetupHelpers(t *testing.T) {
	ps := []map[string]any{{"key": "OPS"}, {"key": "ORGA"}, {"key": "ZEIT"}, {"key": "X", "name": "Time tracking"}, {"key": "A1"}, {"key": "A2"}, {"key": "A3"}, {"key": "A4"}, {"key": "A5"}}
	r := rankProjects(ps)
	if len(r) != 8 || r[0]["key"] != "ZEIT" || r[1]["key"] != "X" || r[2]["key"] != "ORGA" {
		t.Errorf("Rangfolge: %v", r)
	}
	var w map[string]any
	_ = json.Unmarshal([]byte(`{"attributes":{"_Typ_":{"name":"Typ","value":"x"},"_Kunde_":{"name":"Kunden-Account","value":"KD1"}}}`), &w)
	if worklogAccount(w) != "KD1" {
		t.Errorf("Account über Name: %q", worklogAccount(w))
	}
	_ = json.Unmarshal([]byte(`{"attributes":{"_Account_":{"value":"IMP"}}}`), &w)
	if worklogAccount(w) != "IMP" {
		t.Error("_Account_")
	}
	var acc any
	_ = json.Unmarshal([]byte(`[{"key":"A","name":"a","status":"OPEN"},{"key":"B","status":"CLOSED"},{"key":"C"}]`), &acc)
	if l := openAccounts(acc); len(l) != 2 || l[0]["key"] != "A" || l[1]["key"] != "C" {
		t.Errorf("offene Accounts: %v", l)
	}
	if topKey(map[string]float64{"b": 2, "a": 2, "c": 1}) != "a" || topKey(nil) != "" {
		t.Error("topKey stabil")
	}
	if nextMonth("2026-12") != "2027-01" || nextMonth("x") != "9999-12" {
		t.Error("nextMonth")
	}
}
