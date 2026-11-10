package app

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Kalender ohne Datenbank: 2026-11 (1.11. ist Sonntag), Feiertag am Mittwoch 18.11.
var testCal = workCal{hol: map[string]bool{"2026-11-18": true}}

func TestAlltagRoutesRegistered(t *testing.T) {
	mux := http.NewServeMux()
	(&App{}).Routes(mux)
	for _, p := range [][2]string{
		{"GET", "/api/todo"}, {"GET", "/api/todo/range"}, {"POST", "/api/todo"}, {"PUT", "/api/todo/3"}, {"DELETE", "/api/todo/3"},
		{"POST", "/api/todo/3/start"}, {"PUT", "/api/todo-settings"}, {"GET", "/api/todo/suggest"}, {"POST", "/api/todo/suggest/dismiss"},
		{"GET", "/api/todo/templates"}, {"POST", "/api/todo/templates"}, {"DELETE", "/api/todo/templates/2"},
		{"GET", "/api/assistant"}, {"PUT", "/api/assistant"}, {"GET", "/api/assistant/tasks"}, {"POST", "/api/assistant/reset"},
		{"GET", "/api/workday"}, {"POST", "/api/workday/start"}, {"PUT", "/api/workday/4"}, {"POST", "/api/workday/end"}, {"POST", "/api/workday/reopen"},
		{"POST", "/api/workday/pause/start"}, {"POST", "/api/workday/pause/end"},
		{"GET", "/api/month-end"}, {"POST", "/api/month-end/ok"}, {"GET", "/api/missing-hours"},
		{"GET", "/api/gap-suggest"}, {"POST", "/api/gap-suggest/learn"}, {"GET", "/api/suggest-now"},
		{"GET", "/api/absence-hints"}, {"POST", "/api/absence-hints/abc"}, {"GET", "/api/ticket-estimates"}, {"PUT", "/api/ticket-estimates/AB-1"},
	} {
		req, _ := http.NewRequest(p[0], p[1], nil)
		if _, pat := mux.Handler(req); pat == "" {
			t.Errorf("%s %s: keine Route", p[0], p[1])
		}
	}
	for pat := range emergencyAllowed { // Notfallzugang: keine Alltags-Route
		for _, f := range []string{"/api/todo", "/api/assistant", "/api/workday", "/api/month-end", "/api/gap-suggest", "/api/suggest-now"} {
			if strings.Contains(pat, f) {
				t.Errorf("Notfallzugang darf %q nicht", pat)
			}
		}
	}
}

func TestSuggestSwitchFourLevels(t *testing.T) {
	for _, tc := range []struct {
		kind, role string
		productive bool
		want       bool
	}{
		{"local", "admin", false, false}, // technisches Admin-Konto
		{"local", "user", true, true},
		{"oidc", "admin", true, false}, // Admin-Gruppe: nie Zeiterfassung (keine Doppelrolle)
		{"oidc", "user", false, true},  // OIDC/LDAP-Mitarbeiter zeiterfassend
		{"ldap", "user", false, true},
		{"oidc", "lead", true, true},
		{"ldap", "breakglass", true, false},
	} {
		if got := tracksTime(tc.kind, tc.role, tc.productive); got != tc.want {
			t.Errorf("tracksTime(%s,%s,%v)=%v", tc.kind, tc.role, tc.productive, got)
		}
	}
	for _, tc := range []struct {
		tracking bool
		server   string
		blocked  bool
		user     string
		on       bool
		why      string
	}{
		{true, "", false, "1", true, ""},
		{false, "1", false, "1", false, "Konten, die Zeiten erfassen"}, // Stufe 0: Konto
		{false, "0", true, "0", false, "Konten, die Zeiten erfassen"},  // Konto zuerst
		{true, "0", false, "1", false, "Server"},
		{true, "1", true, "1", false, "Administrator"},
		{true, "1", false, "0", false, "Einstellungen"},
	} {
		on, why := suggestOnFor("To-do-Vorschläge", tc.tracking, tc.server, tc.blocked, tc.user)
		if on != tc.on || !strings.Contains(why, tc.why) || (!on && !strings.HasPrefix(why, "To-do-Vorschläge")) {
			t.Errorf("%+v: %v %q", tc, on, why)
		}
	}
}

func TestTodoWhenAndWeeks(t *testing.T) {
	if isoWeekOf("2026-11-10") != "2026-W46" || isoWeekOf("2027-01-01") != "2026-W53" {
		t.Errorf("ISO-Woche: %s %s", isoWeekOf("2026-11-10"), isoWeekOf("2027-01-01"))
	}
	if weekEnd("2026-11-10") != "2026-11-15" || weekEnd("2026-11-15") != "2026-11-15" {
		t.Error("weekEnd = Sonntag")
	}
	for _, tc := range []struct {
		w          string
		date, week any
	}{
		{"today", "2026-11-17", "2026-W47"},
		{"tomorrow", "2026-11-19", "2026-W47"}, // 18.11. ist Feiertag
		{"week", nil, "2026-W47"},
		{"nextweek", nil, "2026-W48"},
		{"2026-12-01", "2026-12-01", "2026-W49"},
		{"", nil, nil},
		{"2026-13-01", nil, nil},
	} {
		d, w := todoWhen(tc.w, "2026-11-17", testCal)
		if d != tc.date || w != tc.week {
			t.Errorf("%q: %v %v", tc.w, d, w)
		}
	}
	if testCal.next("2026-11-20") != "2026-11-23" {
		t.Error("Freitag → Montag")
	}
}

func TestTodoDueFor(t *testing.T) {
	cal := workCal{hol: map[string]bool{"2026-11-30": true, "2026-11-02": true}}
	for _, tc := range []struct{ rule, ref, want string }{
		{"month-last-workday", "2026-11-10", "2026-11-27"}, // 30. Feiertag, 28./29. Wochenende
		{"month-first-workday", "2026-11-10", "2026-11-03"},
		{"month-25", "2026-11-10", "2026-11-25"},
		{"month-28", "2026-02-10", "2026-03-02"}, // 28.02.2026 ist Samstag → nächster Arbeitstag
		{"week-1", "2026-11-12", "2026-11-09"},
		{"week-5", "2026-11-15", "2026-11-13"}, // Sonntag gehört zur Woche ab Montag 9.11.
		{"day", "2026-11-14", ""},
		{"day", "2026-11-13", "2026-11-13"},
		{"quatsch", "2026-11-13", ""},
	} {
		if got := todoDueFor(tc.rule, tc.ref, cal); got != tc.want {
			t.Errorf("%s @%s: %q, erwartet %q", tc.rule, tc.ref, got, tc.want)
		}
	}
	if !todoRuleRE.MatchString("month-28") || todoRuleRE.MatchString("month-29") || todoRuleRE.MatchString("week-6") {
		t.Error("Rhythmus-Prüfung")
	}
}

func TestTodoColumn(t *testing.T) {
	const td, tm, we, wk = "2026-11-17", "2026-11-19", "2026-11-22", "2026-W47"
	for _, tc := range []struct{ sched, week, due, want string }{
		{"2026-11-16", "", "", "overdue"},
		{"2026-11-20", "", "2026-11-16", "overdue"},
		{td, "", "", "today"},
		{tm, "", "", "tomorrow"},
		{"2026-11-21", "", "", "week"},
		{"", wk, "", "week"},
		{"2026-11-30", "", "", "later"},
		{"", "2026-W48", "", "later"},
		{"", "", "", "inbox"},
	} {
		if got := todoColumn(tc.sched, tc.week, tc.due, td, tm, we, wk); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
}

func TestTicketSuggestion(t *testing.T) {
	const td, tm, we = "2026-11-17", "2026-11-18", "2026-11-22"
	for _, tc := range []struct {
		due, prio string
		score     int
		why       string
		when      any
	}{
		{"2026-11-10", "", 100, "überfällig seit 2026-11-10", "today"},
		{td, "High", 120, "heute fällig · High", "today"},
		{tm, "", 80, "morgen fällig", "tomorrow"},
		{"2026-11-21", "", 50, "fällig 2026-11-21", "week"},
		{"2026-12-30", "", 10, "fällig 2026-12-30", "later"},
		{"", "Highest", 40, "Highest", "week"},
		{"", "", 0, "dir zugewiesen", "later"},
	} {
		s, w, wh := ticketSuggestion(tc.due, tc.prio, td, tm, we)
		if s != tc.score || w != tc.why || wh != tc.when {
			t.Errorf("%+v: %d %q %v", tc, s, w, wh)
		}
	}
}

func TestMissingDays(t *testing.T) {
	tg := [7]float64{0, 8, 8, 8, 8, 8, 0}
	h4 := 4.0
	abs := []absRange{{"2026-11-03", "2026-11-03", "urlaub", &h4}, {"2026-11-04", "2026-11-04", "feiertag", nil}, {"2026-11-05", "2026-11-05", "krankheit", nil}}
	booked := map[string]float64{"2026-11-02": 8 * 3600, "2026-11-03": 3.9 * 3600, "2026-11-06": 7.8 * 3600, "2026-11-09": 7 * 3600}
	got := missingDays("2026-11-01", "2026-11-09", tg, abs, booked, map[string]bool{"2026-11-09": true}, nil)
	dates := []string{}
	for _, d := range got {
		dates = append(dates, d.Date)
	}
	// 02.: voll · 03.: 4 h Urlaub + 3,9 gebucht → 0,1 fehlt (< 0,25) · 04.: Feiertag · 05.: krank ganztags · 06.: 0,2 (< 0,25) · 07./08.: Wochenende · 09.: 1 h, „passt so“
	if !reflect.DeepEqual(dates, []string{"2026-11-09"}) || !got[0].OK || got[0].MissingH != 1 || got[0].TargetH != 8 {
		t.Fatalf("Tage: %+v", got)
	}
	if n := len(missingDays("2026-11-01", "2026-11-09", tg, abs, booked, nil, map[string]bool{"2026-11": true})); n != 0 {
		t.Errorf("gesperrter Monat darf nicht zum Nachtragen erscheinen (%d)", n)
	}
	if len(openOnly(got)) != 0 || sumMissing(got) != 1 {
		t.Error("open_days / Summe")
	}
}

func TestMissingHourDays(t *testing.T) {
	tg := [7]float64{0, 8, 8, 8, 8, 8, 0}
	booked := map[string]float64{"2026-11-16": 6 * 3600}
	out := missingHourDays("2026-11-18", 3, 0.25, "2026-01-01", tg, nil, booked, nil)
	if len(out) != 2 || out[0].Date != "2026-11-16" || out[0].MissingH != 2 || out[0].Empty || out[1].Date != "2026-11-17" || !out[1].Empty || out[1].Dow != 2 {
		t.Fatalf("%+v", out)
	}
	if len(missingHourDays("2026-11-18", 3, 0.25, "2026-01-01", tg, nil, booked, map[string]bool{"2026-11": true})) != 0 {
		t.Error("abgeschlossener/gesperrter Monat")
	}
	if len(missingHourDays("2026-11-18", 3, 0.25, "2026-11-17", tg, nil, booked, nil)) != 1 {
		t.Error("vor Buchungsbeginn zählt nicht")
	}
}

func TestToggleDayAndText(t *testing.T) {
	l := toggleDay([]string{"2026-11-05", "x", "2026-11-01"}, "2026-11-03", false)
	if !reflect.DeepEqual(l, []string{"2026-11-01", "2026-11-03", "2026-11-05"}) {
		t.Errorf("%v", l)
	}
	if l = toggleDay(l, "2026-11-03", true); len(l) != 2 {
		t.Errorf("undo: %v", l)
	}
	title, notes, est := gapTodoText([]missDay{{Date: "2026-11-02", MissingH: 1}, {Date: "2026-11-03", MissingH: 1.5}, {Date: "2026-11-04", MissingH: 2}, {Date: "2026-11-05", MissingH: 0.25}})
	if title != "Zeiten nachtragen (4 Tage)" || notes != "05.11. fehlen 0,25 h · 04.11. fehlen 2 h · 03.11. fehlen 1,5 h + 1 weitere" || est != 40 {
		t.Errorf("%q %q %d", title, notes, est)
	}
	if fmtDE(1234.5) != "1234,5" || fmtDE(0.333333) != "0,333" {
		t.Error("fmtDE")
	}
}

func TestCutIntervals(t *testing.T) {
	got := cutIntervals([][2]int{{480, 720}}, [][2]int{{500, 520}, {600, 715}, {100, 200}}, 10)
	if !reflect.DeepEqual(got, [][2]int{{480, 500}, {520, 600}}) { // Rest 715–720 < 10 min fällt weg
		t.Errorf("%v", got)
	}
	if len(cutIntervals([][2]int{{480, 490}}, [][2]int{{470, 500}}, 10)) != 0 {
		t.Error("ganz überdeckt")
	}
}

func TestTypicalDayAndCands(t *testing.T) {
	a := candEntry{TaskID: 1, ProjectID: 10, TaskTitle: "Betrieb", JiraTicket: "OPS-1"}
	b := candEntry{ProjectID: 20, ProjName: "Intern"}
	var hist []candEntry
	for w := 1; w <= 6; w++ {
		x := a
		x.S, x.E, x.Week = 9*60, 10*60, w
		if w <= 3 { // dreimal 9–10 Uhr OPS-1 → Block; „Intern“ nur zweimal → kein Block
			hist = append(hist, x)
		}
		if w <= 2 {
			y := b
			y.S, y.E, y.Week = 11*60, 12*60, w
			hist = append(hist, y)
		}
	}
	bl := typicalDay(hist, 120)
	if len(bl) != 1 || bl[0].S != 540 || bl[0].E != 600 || bl[0].Entry.TaskID != 1 {
		t.Fatalf("%+v", bl)
	}
	if bl = typicalDay(hist, 30); len(bl) != 1 || bl[0].E != 570 {
		t.Errorf("auf Bedarf begrenzt: %+v", bl)
	}
	info := map[string]candEntry{}
	m := newCands(info)
	m.bump(&b, 1, "x")
	m.bump(&a, 3, "davor")
	m.bump(&a, 0.25, "davor")
	m.bump(&candEntry{}, 9, "ohne Projekt zählt nicht")
	c := m.clone()
	c.bump(&b, 5, "y")
	top := m.top(4)
	if len(top) != 2 || top[0]["task_id"] != int64(1) || top[0]["label"] != "OPS-1 Betrieb" || top[0]["score"] != 3.3 || len(top[0]["why"].([]string)) != 1 || top[1]["task_id"] != nil {
		t.Errorf("Rangfolge/Form: %v", top)
	}
	if c.top(1)[0]["project_id"] != int64(20) || m.m["p20"].score != 1 {
		t.Error("clone darf das Original nicht ändern")
	}
	now := newCands(info)
	nowBump(now, hist, 9*60+30)
	if now.m["t1"] == nil || now.m["p20"] != nil || r1(now.m["t1"].score) != 5.2 { // 2,5 + 1,5 + 1,17 (Woche 1–3)
		t.Errorf("jetzt: %+v", now.m)
	}
	if slotKey("2026-11-16", 9*60+59) != "slot:1:9" {
		t.Error("slotKey")
	}
}

func TestOpenPointsAndMonthEndHint(t *testing.T) {
	f := &asstFacts{mode: "bridge", plane: true, jira: true, jiraOn: true, pending: 2, gone: []string{"A", "B", "C", "D"}, onlyJ: []string{"X"}, active: 3, inPlane: 2, push: true, targets: false}
	var steps []string
	for _, o := range openPoints(f) {
		steps = append(steps, o.Step+":"+o.Level)
	}
	want := []string{"projects:todo", "projects:warn", "projects:todo", "tempo:todo", "migration:info", "worktime:todo"}
	if !reflect.DeepEqual(steps, want) {
		t.Errorf("%v", steps)
	}
	if p := openPoints(&asstFacts{targets: true}); len(p) != 1 || p[0].Step != "mode" {
		t.Errorf("ohne Betriebsart: %v", p)
	}
	me := &monthEnd{MonthName: "November", Remind: true, WorkdaysLeft: 1, LastWorkday: "2026-11-30", OpenDays: []missDay{{Date: "2026-11-02", MissingH: 1.3}}, MissingH: 1.3}
	h := monthEndHint(me)
	if h == nil || h.Level != "warn" || h.Title != "Monatsende November: noch 1 Arbeitstag (bis 30.11.)" || h.Text != "Zeiten eintragen – an 1 Tag fehlen 1,25 h" {
		t.Errorf("%+v", h)
	}
	if monthEndHint(&monthEnd{}) != nil {
		t.Error("nichts offen → kein Hinweis")
	}
}

func TestTodoValue(t *testing.T) {
	for _, tc := range []struct {
		f   string
		v   any
		out any
		bad bool
	}{
		{"jira_ticket", " ab-1 ", "AB-1", false},
		{"jira_ticket", "", nil, false},
		{"due_date", "2026-11-31", nil, true},
		{"due_date", "", nil, false},
		{"block_start", "9:00", nil, true},
		{"block_start", "09:30", "09:30", false},
		{"priority", 4.0, nil, true},
		{"priority", 1.0, 1, false},
		{"estimated_minutes", 0.0, 0, false},
		{"estimated_minutes", -1.0, nil, true},
		{"project_id", "x", nil, true},
		{"project_id", 0.0, nil, false},
		{"title", "  ", nil, true},
	} {
		got, msg := todoValue(tc.f, tc.v)
		if (msg != "") != tc.bad || (!tc.bad && got != tc.out) {
			t.Errorf("%s %v: %v %q", tc.f, tc.v, got, msg)
		}
	}
	s := ticketSys{map[string]bool{"PRJA": true}, map[string]bool{"OPS-7": true}}
	if s.of("prja-3") != "plane" || s.of("OPS-7") != "plane" || s.of("OPS-8") != "jira" || s.of("kein") != nil {
		t.Error("Ticket-System")
	}
}

func TestAssistantOffShape(t *testing.T) {
	m := assistantOff("aus")
	for _, k := range []string{"plane", "jira", "sources", "projects", "tempo", "meetings", "ssh", "worktime", "done", "open"} {
		if m[k] == nil {
			t.Errorf("Feld %s fehlt (Oberfläche greift darauf zu)", k)
		}
	}
	if m["enabled"] != false || m["projects"].(map[string]any)["only_jira"] == nil {
		t.Error("Form")
	}
}
