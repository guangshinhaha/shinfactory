package config

import "testing"

func TestDefaultRolesRouteByCost(t *testing.T) {
	cfg := DefaultConfig()
	want := map[Role]string{
		RoleScheduler: "claude-haiku-5-5",
		RoleDebrief:   "claude-haiku-5-5",
		RoleBuilder:   "claude-sonnet-5-5",
		RoleExplorer:  "claude-sonnet-5-5",
		RoleReviewer:  "claude-sonnet-5-5",
		RoleAdversary: "claude-sonnet-5-5",
		RolePlanner:   "claude-opus-5-5",
		RoleVerifier:  "claude-opus-5-5",
	}
	for role, model := range want {
		if got := cfg.PolicyForRole(role); got.Provider != "claude" || got.Model != model {
			t.Errorf("%s = %+v, want claude/%s", role, got, model)
		}
	}
}

func TestTaskKeysResolveToRoles(t *testing.T) {
	cases := map[string]Role{
		"schedule": RoleScheduler, "plan": RolePlanner, "execute": RoleBuilder,
		"explore": RoleExplorer, "quality": RoleReviewer,
		"adversarial-review": RoleAdversary, "verify": RoleVerifier, "debrief": RoleDebrief,
	}
	for key, role := range cases {
		if got, ok := RoleForTask(key); !ok || got != role {
			t.Errorf("RoleForTask(%q) = %q,%v want %q", key, got, ok, role)
		}
	}
	if _, ok := RoleForTask("meditate"); ok {
		t.Error("unrouted task key should fall back to routing.defaults")
	}
}

func TestLookupTaskSeparatesRoutedUnroutedAndUnknown(t *testing.T) {
	if role, routing := LookupTask(" Execute "); routing != TaskRouted || role != RoleBuilder {
		t.Errorf("execute = %q,%v want builder,routed", role, routing)
	}
	if role, routing := LookupTask("meditate"); routing != TaskUnrouted || role != "" {
		t.Errorf("meditate = %q,%v want unrouted", role, routing)
	}
	if _, routing := LookupTask("no-such-task"); routing != TaskUnknown {
		t.Errorf("no-such-task = %v want unknown", routing)
	}
}

func TestValidateStageRoutingRejectsProviderWithoutModel(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.ValidateStageRouting("codex", ""); err == nil {
		t.Error("provider with no model should be rejected")
	}
	for _, c := range [][2]string{{"", ""}, {"", "claude-opus-5-5"}, {"codex", "gpt-5.4"}} {
		if err := cfg.ValidateStageRouting(c[0], c[1]); err != nil {
			t.Errorf("ValidateStageRouting(%q,%q) = %v, want nil", c[0], c[1], err)
		}
	}
}

func parseRouting(t *testing.T, toml string) Config {
	t.Helper()
	cfg, _, err := parseWithDiagnostics([]byte(toml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg
}

func TestPartialRolesTableKeepsOtherDefaults(t *testing.T) {
	cfg := parseRouting(t, "[routing.roles]\nbuilder = { provider = \"claude\", model = \"claude-haiku-5-5\" }\n")
	if got := cfg.PolicyForRole(RoleBuilder).Model; got != "claude-haiku-5-5" {
		t.Errorf("builder = %q", got)
	}
	if got := cfg.PolicyForRole(RolePlanner).Model; got != "claude-opus-5-5" {
		t.Errorf("planner = %q, want default opus", got)
	}
}

func TestCodexOnlyWhenConfigNamesIt(t *testing.T) {
	plain := parseRouting(t, "")
	provider, model := plain.ResolveStageRouting("execute", "codex", "gpt-5.4")
	if provider != "claude" || model != "claude-sonnet-5-5" {
		t.Errorf("unnamed codex leaked: %s/%s", provider, model)
	}

	named := parseRouting(t, "[routing.roles]\nbuilder = { provider = \"codex\", model = \"gpt-5.4\" }\n")
	provider, model = named.ResolveStageRouting("execute", "", "")
	if provider != "codex" || model != "gpt-5.4" {
		t.Errorf("named codex builder = %s/%s", provider, model)
	}
	provider, _ = named.ResolveStageRouting("quality", "", "")
	if provider != "claude" {
		t.Errorf("reviewer should stay claude, got %s", provider)
	}
}

func TestSensitiveRepoNeverRoutesToSecondProvider(t *testing.T) {
	cfg := parseRouting(t, "[routing]\nsensitive = true\n[routing.roles]\nbuilder = { provider = \"codex\", model = \"gpt-5.4\" }\n")
	if got := Validate(cfg); !hasCode(got, DiagnosticCodeSensitiveProvider) {
		t.Errorf("expected %s diagnostic, got %+v", DiagnosticCodeSensitiveProvider, got.Diagnostics)
	}
	for _, key := range []string{"execute", "quality", "plan", "meditate"} {
		provider, _ := cfg.ResolveStageRouting(key, "codex", "gpt-5.4")
		if provider != "claude" {
			t.Errorf("%s routed to %s on a sensitive repo", key, provider)
		}
	}
}

func TestUnknownRoleWarnsAndIsIgnored(t *testing.T) {
	_, diags, err := parseWithDiagnostics([]byte("[routing.roles]\nchef = { provider = \"claude\", model = \"x\" }\n"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range diags {
		if d.FieldPath == "routing.roles.chef" {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for unknown role: %+v", diags)
	}
}

func hasCode(result ValidationResult, code string) bool {
	for _, d := range result.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestStageModelRules(t *testing.T) {
	named := parseRouting(t, "[routing.roles]\nverifier = { provider = \"codex\", model = \"gpt-5.4\" }\n")
	// A stage's own model is kept when it names no provider.
	if _, model := named.ResolveStageRouting("execute", "", "claude-opus-5-5"); model != "claude-opus-5-5" {
		t.Errorf("stage model dropped: %q", model)
	}
	// A named other provider without a model would get a foreign model name: ignored.
	if provider, model := named.ResolveStageRouting("execute", "codex", ""); provider != "claude" || model != "claude-sonnet-5-5" {
		t.Errorf("codex without model = %s/%s", provider, model)
	}
	if provider, model := named.ResolveStageRouting("execute", "codex", "gpt-5.4"); provider != "codex" || model != "gpt-5.4" {
		t.Errorf("named codex with model = %s/%s", provider, model)
	}
}

func TestNonClaudeDefaultsGetNoClaudeRoles(t *testing.T) {
	cfg := parseRouting(t, "[routing]\nsensitive = true\n[routing.defaults]\nprovider = \"codex\"\nmodel = \"gpt-5.4\"\n")
	if got := Validate(cfg); hasCode(got, DiagnosticCodeSensitiveProvider) {
		t.Errorf("default roles flagged: %+v", got.Diagnostics)
	}
	if p, m := cfg.ResolveStageRouting("plan", "", ""); p != "codex" || m != "gpt-5.4" {
		t.Errorf("planner = %s/%s", p, m)
	}
}

func TestZcodeRoleIsAccepted(t *testing.T) {
	cfg := parseRouting(t, "[routing.roles]\nbuilder = { provider = \"zcode\", model = \"GLM-5.3\" }\n")
	if got := Validate(cfg); hasCode(got, DiagnosticCodeProviderUnknown) {
		t.Errorf("zcode role flagged as unknown provider: %+v", got.Diagnostics)
	}
}

func TestRoleWithProviderButNoModelIsFatal(t *testing.T) {
	cfg := parseRouting(t, "[routing.roles]\nbuilder = { provider = \"codex\" }\n")
	if got := Validate(cfg); !hasCode(got, DiagnosticCodeRoutingDefaultsMissing) {
		t.Errorf("expected fatal for role without model: %+v", got.Diagnostics)
	}
}

func TestCodexDefaultsWithoutModelIsFatalNotClaudeModel(t *testing.T) {
	cfg := parseRouting(t, "[routing.defaults]\nprovider = \"codex\"\n")
	if cfg.Routing.Defaults.Provider != "codex" || cfg.Routing.Defaults.Model != "" {
		t.Fatalf("defaults = %+v, want codex with no model", cfg.Routing.Defaults)
	}
	got := Validate(cfg)
	if !hasCode(got, DiagnosticCodeRoutingDefaultsMissing) {
		t.Fatalf("expected fatal routing_defaults_missing, got %+v", got.Diagnostics)
	}
	for _, d := range got.Diagnostics {
		if d.Code == DiagnosticCodeRoutingDefaultsMissing && d.FieldPath != "routing.defaults" {
			t.Errorf("one missing model should report once, also got %+v", d)
		}
	}
}

func TestClaudeDefaultsWithoutModelGetDefaultModel(t *testing.T) {
	cfg := parseRouting(t, "[routing.defaults]\nprovider = \"claude\"\n")
	if cfg.Routing.Defaults.Model != "claude-sonnet-5-5" {
		t.Errorf("model = %q", cfg.Routing.Defaults.Model)
	}
}

func TestValidateStageProviderOnSensitiveRepo(t *testing.T) {
	cfg := parseRouting(t, "[routing]\nsensitive = true\n")
	if err := cfg.ValidateStageProvider("codex"); err == nil {
		t.Error("codex stage on a sensitive claude repo should be rejected")
	}
	for _, ok := range []string{"", "claude", " Claude "} {
		if err := cfg.ValidateStageProvider(ok); err != nil {
			t.Errorf("provider %q: %v", ok, err)
		}
	}
	open := parseRouting(t, "")
	if err := open.ValidateStageProvider("codex"); err != nil {
		t.Errorf("non-sensitive repo should not reject: %v", err)
	}
}

func TestForeignProvidersOnlyOnSensitiveRepo(t *testing.T) {
	cfg := parseRouting(t, "[routing]\nsensitive = true\n")
	if got := cfg.ForeignProviders("claude"); len(got) != 2 || got[0] != "codex" || got[1] != "zcode" {
		t.Errorf("sensitive claude repo: foreign = %v, want [codex zcode]", got)
	}
	if got := cfg.ForeignProviders("codex"); len(got) != 2 || got[0] != "claude" || got[1] != "zcode" {
		t.Errorf("sensitive codex stage: foreign = %v, want [claude zcode]", got)
	}
	if got := cfg.ForeignProviders("zcode"); len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Errorf("sensitive zcode stage: foreign = %v, want [claude codex]", got)
	}
	cfg.Routing.Sensitive = false
	if got := cfg.ForeignProviders("claude"); got != nil {
		t.Errorf("non-sensitive repo: foreign = %v, want none", got)
	}
}

func TestZcodeOnlyWhenConfigNamesIt(t *testing.T) {
	plain := parseRouting(t, "")
	provider, model := plain.ResolveStageRouting("execute", "zcode", "GLM-5.3")
	if provider != "claude" || model != "claude-sonnet-5-5" {
		t.Errorf("unnamed zcode leaked: %s/%s", provider, model)
	}

	named := parseRouting(t, "[routing.roles]\nbuilder = { provider = \"zcode\", model = \"GLM-5.3\" }\n")
	provider, model = named.ResolveStageRouting("execute", "", "")
	if provider != "zcode" || model != "GLM-5.3" {
		t.Errorf("named zcode builder = %s/%s", provider, model)
	}
	provider, _ = named.ResolveStageRouting("plan", "", "")
	if provider != "claude" {
		t.Errorf("planner should stay claude, got %s", provider)
	}
}
