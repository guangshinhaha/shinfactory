package loop

// Test binaries never reach the real gh: a loop built without a Lander gets
// a fake. Tests that need a specific lander inject it through Dependencies.
func init() {
	defaultLander = func() Lander { return &fakeLander{} }
	fetchBase = func(string, string) (string, error) { return "origin/main", nil }
}
