package loop

import "testing"

// Two different order IDs must never share a branch (and so a PR).
func TestCookBaseNameIsUniquePerOrderID(t *testing.T) {
	long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pairs := [][2]string{{"feat/x", "Feat-X"}, {long + "1", long + "2"}}
	for _, p := range pairs {
		if a, b := cookBaseName(p[0], 0, ""), cookBaseName(p[1], 0, ""); a == b {
			t.Errorf("orders %q and %q share branch %q", p[0], p[1], a)
		}
	}
	if got := cookBaseName("42", 0, ""); got != "42-0-task" {
		t.Errorf("numeric order ID branch = %q, want it unchanged", got)
	}
}
