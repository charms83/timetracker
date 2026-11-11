package app

import "testing"

func TestNormMeetingTitle(t *testing.T) {
	if got := normMeetingTitle("StandUp 12.03.2026 09:00"); got != "standup" {
		t.Errorf("got %q", got)
	}
	if got := normMeetingTitle("  Weekly Sync!! "); got != "weekly sync" {
		t.Errorf("got %q", got)
	}
}

func TestMeetingOverlapMs(t *testing.T) {
	if meetingOverlapMs(0, 100, 50, 150) != 50 {
		t.Error("overlap")
	}
	if meetingOverlapMs(0, 10, 20, 30) != 0 {
		t.Error("no overlap")
	}
}
