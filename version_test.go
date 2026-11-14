package main

import "testing"

func TestRepoName(t *testing.T) {
	if got := repoName("/Users/x/work/gantry/"); got != "gantry" {
		t.Fatalf("repoName = %q, want gantry", got)
	}
}

func TestBuildSHAIsShort(t *testing.T) {
	if got := buildSHA(); got == "" || len(got) > 7 {
		t.Fatalf("buildSHA = %q, want 1-7 chars", got)
	}
}
