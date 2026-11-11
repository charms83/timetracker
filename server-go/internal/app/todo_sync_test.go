package app

import "testing"

func TestTodoSyncParseHash(t *testing.T) {
	w := todoSyncParseHash("")
	if w.Start != "09:00" || w.Secs != 0 || w.Due != nil {
		t.Fatalf("empty: %+v", w)
	}
	w = todoSyncParseHash(`{"due":"2026-11-12","day":"2026-11-11","secs":3600,"start":"10:30"}`)
	if asStr(w.Due) != "2026-11-12" || asStr(w.Day) != "2026-11-11" || w.Secs != 3600 || w.Start != "10:30" {
		t.Fatalf("parsed: %+v", w)
	}
	if !todoSyncEqStr(w.Due, "2026-11-12") || todoSyncEqStr(w.Due, nil) {
		t.Fatal("eq")
	}
}

func TestTempoPlanIDOf(t *testing.T) {
	if tempoPlanIDOf(map[string]any{"id": float64(42)}) != "42" {
		t.Fatal("id number")
	}
	if tempoPlanIDOf([]any{map[string]any{"planId": "99"}}) != "99" {
		t.Fatal("array planId")
	}
	if tempoPlanIDOf(nil) != "" {
		t.Fatal("nil")
	}
}
