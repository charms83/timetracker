package app

import "testing"

func TestPlaceInDay(t *testing.T) {
	// leerer Tag ab 08:00
	s, e, ok := placeInDay(nil, 90, placeDayStartMin, placeDayEndMin)
	if !ok || s != 8*60 || e != 8*60+90 {
		t.Fatalf("leer: %d–%d ok=%v", s, e, ok)
	}
	// Lücke zwischen 09–10 und 11–12 → 10:00–10:45
	taken := [][2]int{{9 * 60, 10 * 60}, {11 * 60, 12 * 60}}
	s, e, ok = placeInDay(taken, 45, placeDayStartMin, placeDayEndMin)
	if !ok || s != 8*60 || e != 8*60+45 {
		t.Fatalf("vor erster Buchung: %d–%d", s, e)
	}
	taken = [][2]int{{8 * 60, 10 * 60}, {11 * 60, 12 * 60}}
	s, e, ok = placeInDay(taken, 45, placeDayStartMin, placeDayEndMin)
	if !ok || s != 10*60 || e != 10*60+45 {
		t.Fatalf("Lücke: %d–%d", s, e)
	}
	// Lücke Mittag 12–13 vor „danach“
	taken = [][2]int{{8 * 60, 12 * 60}, {13 * 60, 17 * 60}}
	s, e, ok = placeInDay(taken, 30, placeDayStartMin, placeDayEndMin)
	if !ok || s != 12*60 || e != 12*60+30 {
		t.Fatalf("Mittagslücke: %d–%d", s, e)
	}
	// hinter letzter Buchung, wenn keine Lücke groß genug
	taken = [][2]int{{8 * 60, 12 * 60}, {12 * 60, 17 * 60}}
	s, e, ok = placeInDay(taken, 30, placeDayStartMin, placeDayEndMin)
	if !ok || s != 17*60 || e != 17*60+30 {
		t.Fatalf("danach: %d–%d", s, e)
	}
	if _, _, ok = placeInDay(nil, 0, placeDayStartMin, placeDayEndMin); ok {
		t.Fatal("Dauer 0 muss scheitern")
	}
}
