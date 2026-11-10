package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPlanImportRules(t *testing.T) {
	loc := berlin(t)
	at := func(s string) time.Time { x, _ := time.Parse(time.RFC3339, s); return x }
	wls := []planeWL{
		{ID: "w1", WI: "wi1", Key: "PRJA-1", Date: "2026-10-06", Mins: 90, Created: at("2026-10-06T15:00:00Z")},
		{ID: "w2", WI: "wi1", Key: "PRJA-1", Date: "2026-10-06", Mins: 30, Created: at("2026-10-06T09:00:00Z")}, // früher → zuerst gestapelt
		{ID: "w3", WI: "wi2", Key: "PRJA-2", Date: "2026-10-06", Mins: 60},                                      // lokal vollständig erfasst
		{ID: "w4", WI: "wi3", Key: "PRJA-3", Date: "2026-10-06", Mins: 60},                                      // lokal nur teilweise
		{ID: "w5", WI: "wi1", Key: "PRJA-1", Date: "2026-10-07", Mins: 15},                                      // schon importiert
		{ID: "w6", WI: "wi1", Key: "PRJA-1", Date: "2026-09-30", Mins: 15},                                      // Monat freigegeben
		{ID: "w7", WI: "wi1", Key: "PRJA-1", Date: "2026-12-01", Mins: 15},                                      // außerhalb
		{ID: "w8", WI: "wi9", Date: "2026-10-08", Mins: 45},                                                     // ohne Key: über Work-Item
	}
	known := map[string]string{"w5": "schon importiert"}
	local := map[string]int64{"PRJA-2|2026-10-06": 3600, "PRJA-3|2026-10-06": 1800, "wi:wi9|2026-10-08": 0}
	locked := map[string]bool{"2026-09": true}
	used := map[string]int64{"2026-10-06": 30} // schon 30 min importiert an dem Tag
	got := map[string]importDecision{}
	for _, d := range planImport(wls, known, local, locked, used, "2026-09-01", "2026-11-30", loc) {
		got[d.WL.ID] = d
	}
	for id, want := range map[string]string{"w1": "", "w2": "", "w3": "schon lokal erfasst", "w4": "teilweise lokal", "w5": "schon importiert",
		"w6": "eingereicht oder freigegeben", "w7": "außerhalb", "w8": ""} {
		d := got[id]
		if (want == "") != (d.Action == "import") || !strings.Contains(d.Reason, want) {
			t.Errorf("%s: %s %q, want %q", id, d.Action, d.Reason, want)
		}
	}
	// Platzhalter ab 08:00 + schon belegte 30 min; w2 (früher erstellt) vor w1
	if s := got["w2"].Start.In(loc).Format("15:04"); s != "08:30" {
		t.Errorf("w2 Start %s", s)
	}
	if s, e := got["w1"].Start.In(loc).Format("15:04"), got["w1"].End.In(loc).Format("15:04"); s != "09:00" || e != "10:30" {
		t.Errorf("w1 %s–%s", s, e)
	}
	if s := got["w8"].Start.In(loc).Format("15:04"); s != "08:00" {
		t.Errorf("w8 Start %s", s)
	}
	// Wiederholung: alles bekannt → nichts mehr
	all := map[string]string{}
	for _, w := range wls {
		all[w.ID] = "schon importiert"
	}
	for _, d := range planImport(wls, all, local, locked, used, "2026-09-01", "2026-11-30", loc) {
		if d.Action == "import" {
			t.Errorf("idempotent: %s würde erneut importiert", d.WL.ID)
		}
	}
	// Schlüssel und Work-Item zeigen auf dieselben Buchungen: Maximum, nicht Summe
	two := planImport([]planeWL{{ID: "x", WI: "wiA", Key: "K-1", Date: "2026-10-06", Mins: 60}}, nil, map[string]int64{"K-1|2026-10-06": 1800, "wi:wiA|2026-10-06": 1800}, nil, nil, "2026-10-01", "2026-10-31", loc)
	if two[0].Action != "skip" || !strings.Contains(two[0].Reason, "teilweise") {
		t.Errorf("Maximum statt Summe: %+v", two[0])
	}
}

func TestPlaneImportHelpers(t *testing.T) {
	loc := berlin(t)
	for desc, own := range map[string]bool{"Zeitprotokoll: 3 Einträge … Summe wird automatisch gepflegt [zp-sum]": true, "Login [zp#42] · 2026-10-01 08:00–09:00": true, "Handarbeit": false} {
		if planeOwnPush(desc) != own {
			t.Errorf("eigene Übertragung %q", desc)
		}
	}
	created, _ := time.Parse(time.RFC3339, "2026-10-05T23:30:00Z")
	if d := planeWLDate("Doku", created, loc); d != "2026-10-06" { // lokaler Tag
		t.Errorf("Datum %s", d)
	}
	if d := planeWLDate("Doku · 2026-10-02 10:00", created, loc); d != "2026-10-02" {
		t.Errorf("Datum aus Text %s", d)
	}
	if d := planeWLDate("Release 2026-12-01 vorbereiten", created, loc); d != "2026-10-06" {
		t.Errorf("beliebiges Datum im Text zählt nicht: %s", d)
	}
	if c := planeImportComment("Doku · 2026-10-02 10:00–11:00"); c != "Doku" {
		t.Errorf("Kommentar %q", c)
	}
	now, _ := time.Parse("2006-01-02", "2026-11-09")
	if f, to, err := planeImportRange("", "", "2026-10-01", now); err != nil || f != "2026-10-01" || to != "2026-11-09" {
		t.Errorf("Standard: %s %s %v", f, to, err)
	}
	if f, _, err := planeImportRange("", "", "2024-01-01", now); err != nil || f != "2025-11-10" {
		t.Errorf("Standard auf 12 Monate gekürzt: %s %v", f, err)
	}
	if _, _, err := planeImportRange("2024-01-01", "2026-01-01", "", now); err == nil {
		t.Error("mehr als 12 Monate ausdrücklich → Fehler")
	}
	if _, _, err := planeImportRange("2026-10-05", "2026-10-01", "", now); err == nil {
		t.Error("to vor from → Fehler")
	}
}

func TestFetchPlaneWorklogs(t *testing.T) {
	loc := berlin(t)
	var hits []string
	srv := startFake(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/workspaces/hon/projects/pp1/total-worklogs/":
			_, _ = io.WriteString(w, `[{"issue_id":"wi1","duration":120},{"issue_id":"wi2","duration":0}]`)
		case "/api/v1/workspaces/hon/projects/pp1/work-items/wi1/worklogs/":
			_, _ = io.WriteString(w, `{"results":[
				{"id":"a","duration":30,"description":"Analyse","logged_by":"me","created_at":"2026-10-06T10:00:00Z"},
				{"id":"b","duration":60,"description":"Zeitprotokoll: … [zp-sum]","logged_by":"me","created_at":"2026-10-06T10:00:00Z"},
				{"id":"c","duration":45,"description":"Kollege","logged_by":"other","created_at":"2026-10-06T10:00:00Z"}]}`)
		case "/api/v1/workspaces/hon/projects/pp1/work-items/wi1/":
			_, _ = io.WriteString(w, `{"id":"wi1","sequence_id":17}`)
		default:
			w.WriteHeader(404)
		}
	}), false)
	old := planeHTTP
	planeHTTP = &http.Client{Transport: &http.Transport{DialContext: memDial}, CheckRedirect: old.CheckRedirect, Timeout: 5 * time.Second}
	defer func() { planeHTTP = old }()
	a := &App{Loc: loc}
	c := planeCfg{Base: srv.URL, Key: "k", WS: "hon"}
	wls, stats, errs := a.fetchPlaneWorklogs(context.Background(), 1, c, []importProject{{ID: 5, Plane: "pp1", Name: "P", Ident: "prja"}}, "me", func(string) string { return "" })
	if len(errs) != 0 || len(wls) != 1 || wls[0].ID != "a" || wls[0].Key != "PRJA-17" || wls[0].Mins != 30 || wls[0].Date != "2026-10-06" || wls[0].ProjectID != 5 {
		t.Fatalf("Worklogs: %+v %v", wls, errs)
	}
	if stats["own_push"] != 1 || stats["other_user"] != 1 || stats["checked"] != 3 {
		t.Errorf("Zählung: %v", stats)
	}
	for _, h := range hits {
		if strings.Contains(h, "wi2") {
			t.Error("Work-Item ohne Dauer nicht abfragen")
		}
		if !strings.HasPrefix(h, "GET ") {
			t.Errorf("Import darf in Plane nichts schreiben: %s", h)
		}
	}
}
