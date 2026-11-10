package loop

import (
	"fmt"
	"os"
)

// Test binaries never reach the real gh: a loop built without a Lander gets
// a fake. Tests that need a specific lander inject it through Dependencies.
func init() {
	ghRunGuard = func(bin string) error {
		if os.Getenv(realGHEnv) == "" {
			return fmt.Errorf("refusing to run %s in a test binary: set %s=1 only in a test that stubs it", bin, realGHEnv)
		}
		return nil
	}
	defaultLander = func() Lander { return &fakeLander{} }
	fetchBase = func(string, string) (string, error) { return "origin/main", nil }
}
