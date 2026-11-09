package orderx

import (
	"strings"
	"testing"

	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/internal/taskreg"
	"github.com/guangshinhaha/shinfactory/skill"
)

func routingOrders(provider string) OrdersFile {
	return OrdersFile{Orders: []Order{{
		ID: "a", Title: "a", Status: OrderStatusActive,
		Stages: []Stage{{TaskKey: "execute", Skill: "execute", Provider: provider, Model: "gpt-5.4", Status: StageStatusPending}},
	}}}
}

func execRegistry() taskreg.Registry {
	return taskreg.NewFromSkills([]skill.SkillMeta{{
		Name: "execute", Path: "/s/execute", Frontmatter: skill.Frontmatter{Schedule: "x"},
	}})
}

func TestSensitiveRepoRejectsStageProviderAtIngest(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Routing.Sensitive = true
	_, _, err := NormalizeAndValidateOrders(routingOrders("codex"), execRegistry(), cfg)
	if err == nil || !strings.Contains(err.Error(), "routing.sensitive") || !strings.Contains(err.Error(), `order "a" stage 0`) {
		t.Fatalf("err = %v, want a routing.sensitive rejection naming order a stage 0", err)
	}
	if _, _, err := NormalizeAndValidateOrders(routingOrders("claude"), execRegistry(), cfg); err != nil {
		t.Errorf("claude stage on a sensitive claude repo: %v", err)
	}
	cfg.Routing.Sensitive = false
	if _, _, err := NormalizeAndValidateOrders(routingOrders("codex"), execRegistry(), cfg); err != nil {
		t.Errorf("non-sensitive repo: %v", err)
	}
}

func TestNormalizeDoesNotWriteResolvedRoutingOntoStages(t *testing.T) {
	cfg := config.DefaultConfig()
	of := routingOrders("")
	of.Orders[0].Stages[0].Model = ""
	got, _, err := NormalizeAndValidateOrders(of, execRegistry(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st := got.Orders[0].Stages[0]; st.Provider != "" || st.Model != "" {
		t.Errorf("stage = %s/%s, want routing left empty", st.Provider, st.Model)
	}
}
