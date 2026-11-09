package taskreg

import (
	"path/filepath"
	"testing"

	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/skill"
)

// Every task type the repo ships either maps to a role or is marked unrouted in
// the config routing table, so a new skill cannot fall back to routing.defaults by
// accident.
func TestBundledTaskTypesAreRoutedOrListedUnrouted(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".agents", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	types, err := skill.Resolver{SearchPaths: []string{root}}.DiscoverTaskTypes()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(types) == 0 {
		t.Fatal("no bundled task types found; test would pass vacuously")
	}
	reg := NewFromSkills(types)
	for _, tt := range reg.All() {
		if _, routing := config.LookupTask(tt.Key); routing == config.TaskUnknown {
			t.Errorf("task type %q is not in the config routing table: map it to a role or mark it unrouted", tt.Key)
		}
	}
	for _, key := range config.KnownTasks() {
		if _, routing := config.LookupTask(key); routing != config.TaskUnrouted {
			continue
		}
		if _, ok := reg.ByKey(key); !ok {
			t.Errorf("config routing table marks %q unrouted but no bundled task type has that key", key)
		}
	}
}
