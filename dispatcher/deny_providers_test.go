package dispatcher

import (
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestClaudeArgsDenyForeignProviderCLIs(t *testing.T) {
	args := claudeBaseArgs(DispatchRequest{Provider: "claude", DenyProviders: []string{"codex"}}, "")
	i := slices.IndexFunc(args, func(a string) bool { return strings.HasPrefix(a, "--disallowedTools=") })
	if i < 0 {
		t.Fatalf("args = %v, want --disallowedTools=<rules>", args)
	}
	if rules := args[i]; !strings.Contains(rules, "Bash(codex:*)") {
		t.Errorf("deny rules = %q, want codex denied", rules)
	}
	if strings.Contains(args[i], "exec*") {
		t.Errorf("deny rules %q match prose like grep 'codex exec'", args[i])
	}
	for _, a := range claudeBaseArgs(DispatchRequest{Provider: "claude"}, "") {
		if strings.HasPrefix(a, "--disallowedTools") {
			t.Error("non-sensitive dispatch must not deny anything")
		}
	}
}

func TestDispatchEnvFlagsSensitiveSession(t *testing.T) {
	env := buildDispatchEnv(DispatchRequest{Provider: "claude", DenyProviders: []string{"codex"}})
	if !slices.Contains(env, "NOODLE_SENSITIVE=1") || !slices.Contains(env, "NOODLE_DENY_PROVIDERS=codex") {
		t.Errorf("env missing sensitive markers")
	}
	for _, e := range buildDispatchEnv(DispatchRequest{Provider: "claude"}) {
		if strings.HasPrefix(e, "NOODLE_SENSITIVE=") {
			t.Errorf("non-sensitive env carries %s", e)
		}
	}
}

func TestOrcaSpecNamesDeniedProvider(t *testing.T) {
	d := &OrcaDispatcher{projectDir: "/p"}
	spec := d.buildSpec("s1", DispatchRequest{Provider: "claude", Prompt: "synthetic", DenyProviders: []string{"codex"}}, "")
	if !strings.Contains(spec, "Do not run or delegate to the codex CLI") {
		t.Errorf("spec lacks the sensitive line:\n%s", spec)
	}
	if strings.Contains(d.buildSpec("s1", DispatchRequest{Provider: "claude", Prompt: "synthetic"}, ""), "Sensitive repo") {
		t.Error("non-sensitive spec mentions a sensitive repo")
	}
}

func TestCodexStageCannotRunClaudeCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh shim")
	}
	dir, err := writeDenyShims(t.TempDir(), []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "claude -p synthetic")
	cmd.Env = []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "routing.sensitive") {
		t.Fatalf("claude ran through the shim: err=%v out=%q", err, out)
	}
	if d, _ := writeDenyShims(t.TempDir(), nil); d != "" {
		t.Errorf("no providers should write no shims, got %q", d)
	}
}
