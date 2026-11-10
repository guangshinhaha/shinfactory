package config

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/stringx"
)

const (
	DefaultConfigPath = ".noodle.toml"
)

// Config is the top-level .noodle.toml contract for runtime wiring.
type Config struct {
	Adapters    map[string]AdapterConfig `toml:"adapters"`
	Routing     RoutingConfig            `toml:"routing"`
	Skills      SkillsConfig             `toml:"skills"`
	Mode        string                   `toml:"mode"`
	Concurrency ConcurrencyConfig        `toml:"concurrency"`
	Agents      AgentsConfig             `toml:"agents"`
	Runtime     RuntimeConfig            `toml:"runtime"`
	Server      ServerConfig             `toml:"server"`
	Landing     LandingConfig            `toml:"landing"`
	Spend       SpendConfig              `toml:"spend"`
	Backlog     BacklogConfig            `toml:"backlog"`
}

const defaultBacklogRetryWindow = 5 * time.Minute

// BacklogConfig tunes how the loop treats a backlog sync that fails with a
// transient network or 5xx error.
type BacklogConfig struct {
	// RetryWindowSeconds is the total time the loop keeps retrying a transient
	// backlog failure, with capped exponential backoff, before it stops.
	// 0 = five minutes.
	RetryWindowSeconds int `toml:"retry_window_seconds"`
}

// RetryWindow is the total time to keep retrying a transient backlog failure.
func (c BacklogConfig) RetryWindow() time.Duration {
	if c.RetryWindowSeconds <= 0 {
		return defaultBacklogRetryWindow
	}
	return time.Duration(c.RetryWindowSeconds) * time.Second
}

const (
	defaultMaxStageAttempts = 3
	defaultMaxOrderCostUSD  = 20.0
)

// SpendConfig caps what one order may spend before the loop stops it and asks
// a person. Both caps are checked before each stage launch.
type SpendConfig struct {
	// MaxAttempts is how many times one order may launch the same stage.
	MaxAttempts int `toml:"max_attempts"`
	// MaxCostUSD is the cost ceiling for one order, summed from the cost its
	// finished stages reported in their session logs.
	MaxCostUSD float64 `toml:"max_cost_usd"`
}

// LandingConfig controls how finished work reaches the default branch.
type LandingConfig struct {
	// Mode is "pr" (one branch per order; push and open a draft PR after the
	// first stage with commits, mark it ready and merge it when the last stage
	// completes) or "local" (merge each stage's worktree into the local default
	// branch). Empty means unset: the loop picks "pr" when the project has a
	// GitHub remote and "local" when it has none. An explicit value always wins.
	Mode string `toml:"mode"`
	// Base is the PR base branch; empty means the repository default.
	Base string `toml:"base"`
	// CloseIssues puts "Closes #<order id>" in the PR body, so merging the PR
	// closes the order's GitHub issue. Set it only when the backlog is this
	// repository's GitHub issues, where an order ID is an issue number.
	CloseIssues bool `toml:"close_issues"`
}

// PR reports whether work lands through a pull request.
func (c LandingConfig) PR() bool { return c.Mode == LandingModePR }

const (
	LandingModeLocal = "local"
	LandingModePR    = "pr"
)

type AdapterConfig struct {
	Skill   string            `toml:"skill"`
	Scripts map[string]string `toml:"scripts"`
}

type RoutingConfig struct {
	// Defaults applies to task keys that have no role.
	Defaults ModelPolicy `toml:"defaults"`
	// Roles maps a role (see Role) to its provider and model.
	Roles map[string]ModelPolicy `toml:"roles"`
	// Sensitive keeps every stage on Defaults.Provider; roles naming another
	// provider are a fatal config error.
	Sensitive bool `toml:"sensitive"`
}

type ModelPolicy struct {
	Provider string `toml:"provider"`
	Model    string `toml:"model"`
}

type SkillsConfig struct {
	Paths []string `toml:"paths"`
}

type ConcurrencyConfig struct {
	MaxConcurrency int `toml:"max_concurrency"`
}

type ProviderConfig struct {
	Path string   `toml:"path"`
	Args []string `toml:"args"`
}

type AgentsConfig struct {
	Claude ProviderConfig `toml:"claude"`
	Codex  ProviderConfig `toml:"codex"`
}

// RuntimeConfig controls the default runtime for spawned cook sessions.
type RuntimeConfig struct {
	Default string        `toml:"default"` // runtime kind, defaults to process
	Process ProcessConfig `toml:"process"`
	Sprites SpritesConfig `toml:"sprites"`
	Cursor  CursorConfig  `toml:"cursor"`
	Orca    OrcaConfig    `toml:"orca"`

	spritesDefined bool
	cursorDefined  bool
	orcaDefined    bool
}

// MaxConcurrentFor returns the per-runtime concurrency cap for a given runtime name.
// Returns 0 when unlimited (no per-runtime cap; the global MaxConcurrency ceiling applies).
func (c RuntimeConfig) MaxConcurrentFor(name string) int {
	switch stringx.Normalize(name) {
	case "process":
		return c.Process.MaxConcurrent
	case "sprites":
		return c.Sprites.MaxConcurrent
	case "cursor":
		return c.Cursor.MaxConcurrent
	case "orca":
		return c.Orca.MaxConcurrent
	default:
		return 0
	}
}

type ProcessConfig struct {
	MaxConcurrent int `toml:"max_concurrent"`
}

type SpritesConfig struct {
	TokenEnv      string `toml:"token_env"`
	BaseURL       string `toml:"base_url"`
	SpriteName    string `toml:"sprite_name"`
	GitTokenEnv   string `toml:"git_token_env"`
	MaxConcurrent int    `toml:"max_concurrent"`
}

// OrcaConfig configures the optional orca runtime, which runs stages as Orca
// workers. It is available only when this table is set and the orca CLI exists.
type OrcaConfig struct {
	Bin               string `toml:"bin"`                 // orca CLI; empty = "orca" on PATH
	RunID             string `toml:"run_id"`              // Orca run for factory workers; empty = the factory creates and remembers its own
	MaxConcurrent     int    `toml:"max_concurrent"`      // 0 = unlimited
	LimitRetrySeconds int    `toml:"limit_retry_seconds"` // pause after a usage-limit failure before the next launch attempt
}

// LimitRetry is how long the loop waits after a usage-limit failure before it
// tries the next orca launch.
func (c OrcaConfig) LimitRetry() time.Duration {
	if c.LimitRetrySeconds <= 0 {
		return defaultOrcaLimitRetry
	}
	return time.Duration(c.LimitRetrySeconds) * time.Second
}

// BinName is the orca CLI to run.
func (c OrcaConfig) BinName() string {
	if bin := strings.TrimSpace(c.Bin); bin != "" {
		return bin
	}
	return "orca"
}

// available reports whether the orca CLI resolves. Without it the factory
// runs exactly as it does without the orca runtime.
func (c OrcaConfig) available() bool {
	_, err := exec.LookPath(c.BinName())
	return err == nil
}

const defaultOrcaLimitRetry = 5 * time.Minute

type CursorConfig struct {
	APIKeyEnv     string `toml:"api_key_env"`
	BaseURL       string `toml:"base_url"`
	Repository    string `toml:"repository"`
	MaxConcurrent int    `toml:"max_concurrent"`
}

// ServerConfig controls the web UI server.
type ServerConfig struct {
	Port    int   `toml:"port"`
	Enabled *bool `toml:"enabled"` // nil = auto (enabled in interactive terminals)
}

type DiagnosticSeverity string

const (
	DiagnosticSeverityRepairable DiagnosticSeverity = "repairable"
	DiagnosticSeverityFatal      DiagnosticSeverity = "fatal"
	DiagnosticSeverityWarning    DiagnosticSeverity = "warning"
)

type ConfigDiagnostic struct {
	FieldPath string
	Message   string
	Severity  DiagnosticSeverity
	Fix       string
	Code      string
	Meta      map[string]string
}

const (
	DiagnosticCodeRoutingDefaultsMissing = "routing_defaults_missing"
	DiagnosticCodeAgentDirMissing        = "agent_dir_missing"
	DiagnosticCodeAdapterSkillMissing    = "adapter_skill_missing"
	DiagnosticCodeAdapterScriptEmpty     = "adapter_script_empty"
	DiagnosticCodeAdapterScriptMissing   = "adapter_script_missing"
	DiagnosticCodeProviderUnknown        = "provider_unknown"
	DiagnosticCodeSensitiveProvider      = "routing_sensitive_provider"
	DiagnosticCodeRuntimeDefaultUnknown  = "runtime_default_unknown"
	DiagnosticCodeNoBacklogAdapter       = "no_backlog_adapter"
	DiagnosticCodeConfigFieldRemoved     = "config_field_removed"
	DiagnosticCodeConfigFieldUnknown     = "config_field_unknown"
	DiagnosticCodeConfigValueNormalized  = "config_value_normalized"
)

type ValidationResult struct {
	Diagnostics []ConfigDiagnostic
}

func (r ValidationResult) CanSpawn() bool {
	return len(r.Fatals()) == 0
}

func (r ValidationResult) Repairables() []ConfigDiagnostic {
	return filterDiagnostics(r.Diagnostics, DiagnosticSeverityRepairable)
}

func (r ValidationResult) Fatals() []ConfigDiagnostic {
	return filterDiagnostics(r.Diagnostics, DiagnosticSeverityFatal)
}

func (r ValidationResult) Warnings() []ConfigDiagnostic {
	return filterDiagnostics(r.Diagnostics, DiagnosticSeverityWarning)
}

func filterDiagnostics(in []ConfigDiagnostic, severity DiagnosticSeverity) []ConfigDiagnostic {
	out := make([]ConfigDiagnostic, 0, len(in))
	for _, diagnostic := range in {
		if diagnostic.Severity == severity {
			out = append(out, diagnostic)
		}
	}
	return out
}

// DefaultConfig returns the full no-config-file defaults.
func DefaultConfig() Config {
	return Config{
		Adapters: defaultAdapters(),
		Routing: RoutingConfig{
			Defaults: ModelPolicy{
				Provider: "claude",
				Model:    defaultRoutingModel,
			},
			Roles: DefaultRoles(ModelPolicy{Provider: "claude", Model: defaultRoutingModel}),
		},
		Skills: SkillsConfig{
			Paths: defaultSkillPaths(),
		},
		Mode: defaultMode,
		Concurrency: ConcurrencyConfig{
			MaxConcurrency: 2,
		},
		Agents: AgentsConfig{},
		Runtime: RuntimeConfig{
			Default: "process",
			Process: ProcessConfig{MaxConcurrent: 4},
			Sprites: SpritesConfig{MaxConcurrent: 50},
			Cursor:  CursorConfig{MaxConcurrent: 10},
		},
		Server: ServerConfig{
			Port: 3000,
		},
		Spend: SpendConfig{
			MaxAttempts: defaultMaxStageAttempts,
			MaxCostUSD:  defaultMaxOrderCostUSD,
		},
	}
}

func defaultAdapters() map[string]AdapterConfig {
	return map[string]AdapterConfig{
		"backlog": {
			Skill: "backlog",
			Scripts: map[string]string{
				"sync": "adapters/backlog-sync",
				"add":  "adapters/backlog-add",
				"done": "adapters/backlog-done",
				"edit": "adapters/backlog-edit",
			},
		},
	}
}

func defaultSkillPaths() []string {
	return []string{".agents/skills"}
}

func (c Config) AvailableRuntimes() []string {
	available := []string{"process"}
	if c.Runtime.spritesDefined && c.Runtime.Sprites.Token() != "" {
		available = append(available, "sprites")
	}
	if c.Runtime.cursorDefined && c.Runtime.Cursor.APIKey() != "" {
		available = append(available, "cursor")
	}
	if c.Runtime.orcaDefined && c.Runtime.Orca.available() {
		available = append(available, "orca")
	}
	return available
}

func (c SpritesConfig) Token() string {
	key := strings.TrimSpace(c.TokenEnv)
	if key == "" {
		key = "SPRITES_TOKEN"
	}
	return strings.TrimSpace(os.Getenv(key))
}

func (c SpritesConfig) GitToken() string {
	key := strings.TrimSpace(c.GitTokenEnv)
	if key == "" {
		key = "GITHUB_TOKEN"
	}
	return strings.TrimSpace(os.Getenv(key))
}

func (c CursorConfig) APIKey() string {
	key := strings.TrimSpace(c.APIKeyEnv)
	if key == "" {
		key = "CURSOR_API_KEY"
	}
	return strings.TrimSpace(os.Getenv(key))
}
