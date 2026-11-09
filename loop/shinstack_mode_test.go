package loop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/guangshinhaha/shinfactory/config"
)

func TestResolveRunMode(t *testing.T) {
	cases := []struct {
		name       string
		claudeMD   *string
		configured string
		want       string
	}{
		{"declared auto beats configured supervised", ptr("# X\n## shinstack\nshinstack-mode: auto\n"), "supervised", "auto"},
		{"declared supervised beats configured auto", ptr("shinstack-mode: supervised\n"), "auto", "supervised"},
		{"declared manual", ptr("shinstack-mode: manual\n"), "auto", "manual"},
		{"undeclared falls back to configured", ptr("# X\nno mode here\n"), "auto", "auto"},
		{"unknown value falls back to configured", ptr("shinstack-mode: yolo\n"), "supervised", "supervised"},
		{"no CLAUDE.md falls back to configured", nil, "manual", "manual"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.claudeMD != nil {
				if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(*tc.claudeMD), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := resolveRunMode(dir, tc.configured); got != tc.want {
				t.Fatalf("resolveRunMode = %q, want %q", got, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestNewLoopRunsInTheReposShinstackMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("shinstack-mode: auto\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig() // factory mode: supervised
	l := New(dir, "noodle", cfg, Dependencies{})
	if l.config.Mode != "auto" || l.canonical.Mode != "auto" {
		t.Fatalf("config mode = %q, canonical mode = %q, want auto for both", l.config.Mode, l.canonical.Mode)
	}

	bare := New(t.TempDir(), "noodle", cfg, Dependencies{})
	if bare.config.Mode != "supervised" {
		t.Fatalf("mode without a declaration = %q, want the factory's supervised", bare.config.Mode)
	}
}
