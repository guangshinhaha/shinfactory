package taskreg

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/guangshinhaha/shinfactory/config"
	"github.com/guangshinhaha/shinfactory/skill"
)

// Every task type the repo ships either maps to a role or is listed in
// config.UnroutedTasks, so a new skill cannot fall back to routing.defaults by
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
		_, routed := config.RoleForTask(tt.Key)
		listed := slices.Contains(config.UnroutedTasks, tt.Key)
		switch {
		case !routed && !listed:
			t.Errorf("task type %q has no role in config.RoleForTask and is not in config.UnroutedTasks", tt.Key)
		case routed && listed:
			t.Errorf("task type %q is both routed and listed in config.UnroutedTasks", tt.Key)
		}
	}
	for _, key := range config.UnroutedTasks {
		if _, ok := reg.ByKey(key); !ok {
			t.Errorf("config.UnroutedTasks lists %q but no bundled task type has that key", key)
		}
	}
}
