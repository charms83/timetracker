package app

import "testing"

func TestServerVersion(t *testing.T) {
	oldV, oldC, oldB := Version, Commit, Built
	defer func() { Version, Commit, Built = oldV, oldC, oldB }()

	Version, Commit, Built = "dev", "", ""
	if ServerVersion() != "go-dev" {
		t.Fatalf("dev: %s", ServerVersion())
	}
	Version = "20261110"
	if ServerVersion() != "go-20261110" {
		t.Fatalf("date: %s", ServerVersion())
	}
	Version = "go-1.2.3"
	if ServerVersion() != "go-1.2.3" {
		t.Fatalf("prefixed: %s", ServerVersion())
	}
}
