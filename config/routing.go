package config

import (
	"sort"
	"strings"

	"github.com/guangshinhaha/shinfactory/internal/stringx"
)

// Role names a job in the kitchen. Each role maps to a provider and model in
// [routing.roles]; the loop resolves a stage's role from its task key.
type Role string

const (
	RoleScheduler Role = "scheduler"
	RolePlanner   Role = "planner"
	RoleBuilder   Role = "builder"
	RoleExplorer  Role = "explorer"
	RoleReviewer  Role = "reviewer"
	RoleAdversary Role = "adversary"
	RoleVerifier  Role = "verifier"
	RoleDebrief   Role = "debrief"
)

const (
	modelLight    = "claude-haiku-5-5"
	modelStandard = "claude-sonnet-5-5"
	modelStrong   = "claude-opus-5-5"
)

// AllRoles lists every role in a stable order.
func AllRoles() []Role {
	return []Role{RoleScheduler, RolePlanner, RoleBuilder, RoleExplorer,
		RoleReviewer, RoleAdversary, RoleVerifier, RoleDebrief}
}

// DefaultRoles is the cost-aware role table used for any role the config omits:
// light for scheduler and debrief, strong only for planner and verifier. A repo
// whose routing.defaults is not claude has no tiers to offer, so every role
// uses its defaults.
func DefaultRoles(defaults ModelPolicy) map[string]ModelPolicy {
	if stringx.Normalize(defaults.Provider) != "claude" {
		out := map[string]ModelPolicy{}
		for _, role := range AllRoles() {
			out[string(role)] = defaults
		}
		return out
	}
	claude := func(model string) ModelPolicy { return ModelPolicy{Provider: "claude", Model: model} }
	return map[string]ModelPolicy{
		string(RoleScheduler): claude(modelLight),
		string(RoleDebrief):   claude(modelLight),
		string(RoleBuilder):   claude(modelStandard),
		string(RoleExplorer):  claude(modelStandard),
		string(RoleReviewer):  claude(modelStandard),
		string(RoleAdversary): claude(modelStandard),
		string(RolePlanner):   claude(modelStrong),
		string(RoleVerifier):  claude(modelStrong),
	}
}

func isKnownRole(name string) bool {
	for _, role := range AllRoles() {
		if string(role) == name {
			return true
		}
	}
	return false
}

// RoleForTask maps a stage's task key (or skill) to its role. Unmapped task
// keys return false and fall back to routing.defaults.
func RoleForTask(taskKey string) (Role, bool) {
	switch stringx.Normalize(taskKey) {
	case "schedule", "bootstrap":
		return RoleScheduler, true
	case "plan":
		return RolePlanner, true
	case "execute", "oops", "commit":
		return RoleBuilder, true
	case "explore", "refine":
		return RoleExplorer, true
	case "quality", "review":
		return RoleReviewer, true
	case "adversarial-review":
		return RoleAdversary, true
	case "verify":
		return RoleVerifier, true
	case "debrief":
		return RoleDebrief, true
	}
	return "", false
}

// PolicyForRole returns the configured provider and model for a role.
func (c Config) PolicyForRole(role Role) ModelPolicy {
	if policy, ok := c.Routing.Roles[string(role)]; ok {
		return c.sensitiveGuard(policy)
	}
	return c.Routing.Defaults
}

// PolicyForTask resolves the policy for a stage's task key.
func (c Config) PolicyForTask(taskKey string) ModelPolicy {
	if role, ok := RoleForTask(taskKey); ok {
		return c.PolicyForRole(role)
	}
	return c.Routing.Defaults
}

// ResolveStageRouting returns the provider and model a stage runs on. A stage's
// own model is honoured. A stage's own provider is honoured only when the config
// names it and the stage also gives a model (a model name is provider-specific).
// Otherwise the stage runs on its role's policy.
func (c Config) ResolveStageRouting(taskKey, stageProvider, stageModel string) (provider, model string) {
	policy := c.PolicyForTask(taskKey)
	provider, model = policy.Provider, policy.Model
	stageModel = strings.TrimSpace(stageModel)
	sp := stringx.Normalize(stageProvider)
	switch {
	case sp == "" || sp == stringx.Normalize(provider):
		if stageModel != "" {
			model = stageModel
		}
	case stageModel != "" && c.providerAllowed(sp):
		provider, model = sp, stageModel
	}
	return provider, model
}

// NamedProviders returns every provider the config names, sorted. When the
// repo is sensitive only the defaults provider counts.
func (c Config) NamedProviders() []string {
	set := map[string]bool{stringx.Normalize(c.Routing.Defaults.Provider): true}
	if !c.Routing.Sensitive {
		for _, policy := range c.Routing.Roles {
			set[stringx.Normalize(policy.Provider)] = true
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (c Config) providerAllowed(provider string) bool {
	for _, name := range c.NamedProviders() {
		if name == provider {
			return true
		}
	}
	return false
}

// sensitiveGuard pins a policy to the defaults provider on a sensitive repo.
func (c Config) sensitiveGuard(policy ModelPolicy) ModelPolicy {
	if c.Routing.Sensitive && stringx.Normalize(policy.Provider) != stringx.Normalize(c.Routing.Defaults.Provider) {
		return ModelPolicy{Provider: c.Routing.Defaults.Provider, Model: c.Routing.Defaults.Model}
	}
	return policy
}

// normalizeRoles fills omitted roles from DefaultRoles and drops unknown ones
// with a warning, so a partial [routing.roles] table is enough.
func normalizeRoles(config *Config, diagnostics *[]ConfigDiagnostic) {
	merged := DefaultRoles(config.Routing.Defaults)
	for name, policy := range config.Routing.Roles {
		key := stringx.Normalize(name)
		if !isKnownRole(key) {
			appendNormalizedValueDiagnostic(diagnostics, "routing.roles."+name,
				"unknown role ignored (valid: scheduler, planner, builder, explorer, reviewer, adversary, verifier, debrief)")
			continue
		}
		policy.Provider = stringx.Normalize(policy.Provider)
		policy.Model = strings.TrimSpace(policy.Model)
		if policy.Provider == "" {
			policy.Provider = merged[key].Provider
		}
		if policy.Model == "" {
			policy.Model = merged[key].Model
		}
		merged[key] = policy
	}
	config.Routing.Roles = merged
}
