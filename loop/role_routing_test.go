package loop

import (
	"context"
	"testing"

	"github.com/guangshinhaha/shinfactory/config"
)

// A fixture run: stages that name no model are dispatched on their role's
// configured model, and a provider the config never names is replaced.
func TestIntegrationStagesDispatchWithRoleModel(t *testing.T) {
	orders := OrdersFile{Orders: []Order{
		{ID: "build", Title: "builder", Status: OrderStatusActive,
			Stages: []Stage{{TaskKey: "execute", Skill: "execute", Status: StageStatusPending}}},
		{ID: "review", Title: "reviewer", Status: OrderStatusActive,
			Stages: []Stage{{TaskKey: "review", Skill: "review", Provider: "codex", Model: "gpt-5.4", Status: StageStatusPending}}},
	}}
	env := newIntegrationEnv(t, orders, func(ic *integrationCfg) {
		ic.cfg.Mode = "auto"
		ic.cfg.Routing.Roles = config.DefaultRoles()
		ic.cfg.Routing.Roles["builder"] = config.ModelPolicy{Provider: "claude", Model: "claude-sonnet-5-5"}
		ic.cfg.Routing.Roles["reviewer"] = config.ModelPolicy{Provider: "claude", Model: "claude-haiku-5-5"}
		ic.cfg.Concurrency.MaxConcurrency = 4
	})
	seedCanonicalFromOrders(env.loop, orders)
	if err := env.loop.Cycle(context.Background()); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	got := map[string][2]string{}
	for _, call := range env.rt.calls {
		got[call.Skill] = [2]string{call.Provider, call.Model}
	}
	want := map[string][2]string{
		"execute": {"claude", "claude-sonnet-5-5"},
		"review":  {"claude", "claude-haiku-5-5"},
	}
	for skill, w := range want {
		if got[skill] != w {
			t.Errorf("%s dispatched on %v, want %v (all: %v)", skill, got[skill], w, got)
		}
	}
}

func TestScheduleOrderUsesSchedulerRole(t *testing.T) {
	cfg := config.DefaultConfig()
	stage := scheduleOrder(cfg, "").Stages[0]
	if stage.Provider != "claude" || stage.Model != "claude-haiku-5-5" {
		t.Fatalf("scheduler stage = %s/%s, want claude/claude-haiku-5-5", stage.Provider, stage.Model)
	}
}
