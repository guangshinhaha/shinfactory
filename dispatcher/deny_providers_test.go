package dispatcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

func TestDenyShimEnvAppliesToEveryProvider(t *testing.T) {
	d := &ProcessDispatcher{runtimeDir: t.TempDir()}
	for provider, denied := range map[string]string{"codex": "claude", "claude": "codex"} {
		env, err := d.denyShimEnv(DispatchRequest{Provider: provider, DenyProviders: []string{denied}})
		if err != nil || len(env) != 1 || !strings.HasPrefix(env[0], "PATH="+filepath.Join(d.runtimeDir, "deny-bin")) {
			t.Fatalf("%s stage env = %v, err = %v, want PATH led by deny-bin", provider, env, err)
		}
	}
	if env, _ := d.denyShimEnv(DispatchRequest{Provider: "codex"}); env != nil {
		t.Errorf("non-sensitive codex stage got %v", env)
	}
}

// A claude stage can build the CLI name at run time and dodge Bash(codex:*);
// the shim catches it at exec, and a custom runtime template inherits the PATH.
func TestClaudeStageCannotReachCodexThroughShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh shim")
	}
	d := &ProcessDispatcher{runtimeDir: t.TempDir(), runtimeDefault: `n=cod; n=${n}ex; "$n" exec synthetic`}
	req := DispatchRequest{Provider: "claude", DenyProviders: []string{"codex"}}
	cmd, err := d.buildCmd(req, "", "")
	if err != nil {
		t.Fatal(err)
	}
	denyEnv, err := d.denyShimEnv(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = denyEnv
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "routing.sensitive") {
		t.Fatalf("codex ran through a variable-built name: err=%v out=%q", err, out)
	}
}

func TestSpriteCommandDeniesForeignCLIs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh shim")
	}
	script := denyShimScript([]string{"codex", " "})
	if denyShimScript(nil) != "" {
		t.Fatal("no providers should give no script")
	}
	cmd := exec.Command("sh", "-c", script+` && codex exec synthetic`)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "routing.sensitive") {
		t.Fatalf("codex ran after the sprite shim script: err=%v out=%q", err, out)
	}
}

func TestOrcaRefusesSensitiveDispatch(t *testing.T) {
	d := &OrcaDispatcher{projectDir: "/p"}
	_, err := d.Dispatch(context.Background(), DispatchRequest{Name: "s", Prompt: "synthetic", Provider: "claude", Model: "m", WorktreePath: "/w", Runtime: "orca", DenyProviders: []string{"codex"}})
	if err == nil || !strings.Contains(err.Error(), "routing.sensitive") {
		t.Fatalf("err = %v, want routing.sensitive refusal", err)
	}
}
