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
		ic.cfg.Routing.Roles = config.DefaultRoles(ic.cfg.Routing.Defaults)
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

// Routing resolves at dispatch only: a queued stage keeps no provider or model
// of its own, so editing [routing.roles] reaches orders already in the queue.
func TestQueuedStagesKeepNoResolvedRouting(t *testing.T) {
	orders := OrdersFile{Orders: []Order{
		{ID: "first", Title: "first", Status: OrderStatusActive,
			Stages: []Stage{{TaskKey: "execute", Skill: "execute", Status: StageStatusPending}}},
		{ID: "queued", Title: "queued", Status: OrderStatusActive,
			Stages: []Stage{{TaskKey: "execute", Skill: "execute", Status: StageStatusPending}}},
	}}
	env := newIntegrationEnv(t, orders, func(ic *integrationCfg) {
		ic.cfg.Mode = "auto"
		ic.cfg.Concurrency.MaxConcurrency = 1
	})
	seedCanonicalFromOrders(env.loop, orders)
	if err := env.loop.Cycle(context.Background()); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	got, err := env.loop.currentOrders()
	if err != nil {
		t.Fatalf("read orders: %v", err)
	}
	for _, o := range got.Orders {
		for _, st := range o.Stages {
			if st.Provider != "" || st.Model != "" {
				t.Errorf("order %s stage %s persisted routing %s/%s, want none", o.ID, st.TaskKey, st.Provider, st.Model)
			}
		}
	}
	if len(env.rt.calls) != 1 || env.rt.calls[0].Model != "claude-sonnet-5-5" {
		t.Fatalf("dispatch calls = %+v, want one on the builder role model", env.rt.calls)
	}

	// The running cook still matches its order's stage, or the next cycle
	// would treat it as superseded and cancel it.
	cook := env.loop.cooks.activeCooksByOrder["first"]
	if cook == nil {
		t.Fatal("no active cook for order first")
	}
	for _, o := range got.Orders {
		if o.ID == "first" && !sameStageDefinition(cook.stage, o.Stages[0]) {
			t.Errorf("cook stage %+v differs from order stage %+v", cook.stage, o.Stages[0])
		}
	}
	if p, m := env.loop.stageRouting(cook.stage); p != "claude" || m != "claude-sonnet-5-5" {
		t.Errorf("snapshot routing = %s/%s, want claude/claude-sonnet-5-5", p, m)
	}

	// Edit the role after the first dispatch; the still-queued stage resolves
	// against the new table.
	env.loop.config.Routing.Roles["builder"] = config.ModelPolicy{Provider: "claude", Model: "claude-haiku-5-5"}
	var queued Stage
	for _, o := range got.Orders {
		if o.ID == "queued" {
			queued = o.Stages[0]
		}
	}
	if _, model := env.loop.config.ResolveStageRouting(queued.TaskKey, queued.Provider, queued.Model); model != "claude-haiku-5-5" {
		t.Errorf("queued stage resolved to %q after role edit, want claude-haiku-5-5", model)
	}
}

func TestScheduleOrderResolvesSchedulerRoleAtDispatch(t *testing.T) {
	cfg := config.DefaultConfig()
	stage := scheduleOrder(cfg, "").Stages[0]
	if stage.Provider != "" || stage.Model != "" {
		t.Fatalf("schedule stage persisted %s/%s, want none", stage.Provider, stage.Model)
	}
	if p, m := cfg.ResolveStageRouting(stage.TaskKey, stage.Provider, stage.Model); p != "claude" || m != "claude-haiku-5-5" {
		t.Fatalf("schedule stage resolves to %s/%s, want claude/claude-haiku-5-5", p, m)
	}
}

// A fixture run on a sensitive repo: an order whose stage names codex never
// reaches the runtime on codex, and every launch carries the deny list.
func TestIntegrationSensitiveRepoStaysOnOneProvider(t *testing.T) {
	orders := OrdersFile{Orders: []Order{
		{ID: "pii", Title: "synthetic", Status: OrderStatusActive,
			Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: "codex", Model: "gpt-5.4", Status: StageStatusPending}}},
	}}
	env := newIntegrationEnv(t, orders, func(ic *integrationCfg) {
		ic.cfg.Mode = "auto"
		ic.cfg.Routing.Sensitive = true
	})
	seedCanonicalFromOrders(env.loop, orders)
	if err := env.loop.Cycle(context.Background()); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if len(env.rt.calls) == 0 {
		t.Fatal("nothing dispatched")
	}
	for _, call := range env.rt.calls {
		if call.Provider != "claude" {
			t.Errorf("%s dispatched on %s, want claude", call.Skill, call.Provider)
		}
		if len(call.DenyProviders) != 1 || call.DenyProviders[0] != "codex" {
			t.Errorf("%s DenyProviders = %v, want [codex]", call.Skill, call.DenyProviders)
		}
	}
}
