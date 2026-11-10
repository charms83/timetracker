package app

import (
	"reflect"
	"testing"
)

func TestDirTeams(t *testing.T) {
	got, bad := dirTeams([]string{"zp-team-Entwicklung", "zp-team-Entwicklung-lead", "zp-team-support", "zp-users", "ZP-TEAM-gross",
		"zp-team-", "zp-team--lead", "zp-team-x\x01y", "zp-team-Support-lead"}, "zp-team-")
	want := map[string]dirTeam{
		"entwicklung": {Name: "Entwicklung", Lead: true},
		"support":     {Name: "support", Lead: true}, // gleiche Kleinschreibung → ein Team, Leitung aus „-lead“
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dirTeams: %v", got)
	}
	if len(bad) != 3 {
		t.Errorf("ungültige Namen: %v", bad)
	}
	if got, _ := dirTeams([]string{"zp-team-a"}, "-"); len(got) != 0 {
		t.Error("Präfix „-“ = aus")
	}
	if got, _ := dirTeams([]string{"zp-team-a"}, ""); len(got) != 0 {
		t.Error("leeres Präfix = aus")
	}
}

func TestTeamDiff(t *testing.T) {
	set, remove := teamDiff(map[int64]bool{1: false, 2: true, 3: false}, map[int64]bool{1: true, 2: true, 4: false})
	if !reflect.DeepEqual(set, map[int64]bool{1: true, 4: false}) || !reflect.DeepEqual(remove, []int64{3}) {
		t.Errorf("set %v, remove %v", set, remove)
	}
	set, remove = teamDiff(map[int64]bool{5: true}, map[int64]bool{})
	if len(set) != 0 || !reflect.DeepEqual(remove, []int64{5}) {
		t.Errorf("alles entfernen: %v %v", set, remove)
	}
}

func TestTeamNameAndPrefix(t *testing.T) {
	for in, ok := range map[string]bool{"A": true, "Team Süd": true, "": false, " x": false, "x ": false, "a\tb": false, string(make([]rune, 81)): false} {
		if validTeamName(in) != ok {
			t.Errorf("validTeamName(%q) != %v", in, ok)
		}
	}
	for in, ok := range map[string]bool{"": true, "-": true, "zp-team-": true, "grp=": false, "a,b": false, "x\x00": false} {
		if (checkTeamPrefix(in) == nil) != ok {
			t.Errorf("checkTeamPrefix(%q)", in)
		}
	}
	if teamPrefix("") != "zp-team-" || teamPrefix(" abt- ") != "abt-" {
		t.Error("teamPrefix")
	}
	if got := groupNames([]string{"CN=zp-team-a,OU=G,DC=x", "zp-team-b", "ou=nur,dc=x"}); !reflect.DeepEqual(got, []string{"zp-team-a", "zp-team-b"}) {
		t.Errorf("groupNames: %v", got)
	}
}
