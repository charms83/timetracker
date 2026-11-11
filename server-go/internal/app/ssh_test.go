package app

import "testing"

func TestSSHEffective(t *testing.T) {
	now := int64(1_700_000_000_000)
	closed := map[string]any{"start_time": "2023-11-14T10:00:00Z", "end_time": "2023-11-14T11:00:00Z", "duration_seconds": 3600, "samples": 0}
	if sshEffective(closed, now) != 3600 {
		t.Errorf("closed window: %d", sshEffective(closed, now))
	}
	measured := map[string]any{"start_time": "2023-11-14T10:00:00Z", "end_time": "2023-11-14T11:00:00Z", "duration_seconds": 3600, "active_seconds": 900, "samples": 5}
	if sshEffective(measured, now) != 900 {
		t.Errorf("measured: %d", sshEffective(measured, now))
	}
}

func TestSSHParseViaDesktopCompat(t *testing.T) {
	// nur Go-seitig: Session-UID-Aggregation wird im Desktop getestet; hier Status-Helfer
	if firstNonEmptyStr("", "a", "b") != "a" {
		t.Error("firstNonEmptyStr")
	}
}
