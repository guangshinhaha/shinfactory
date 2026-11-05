package dispatcher

import "github.com/guangshinhaha/shinfactory/internal/stringx"

// NormalizeRuntime lowercases and trims the runtime string, defaulting to "process".
func NormalizeRuntime(runtime string) string {
	runtime = stringx.Normalize(runtime)
	if runtime == "" {
		return "process"
	}
	return runtime
}
