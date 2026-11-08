package dispatcher

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// usageLimitPattern matches the wording agents and Orca use when the account
// behind a launch has no usage left.
var usageLimitPattern = regexp.MustCompile(
	`(?i)((usage|rate|session|weekly|5-hour|five-hour) limit|limit reached|hit your limit|reached your [a-z0-9 -]*limit|out of (extra )?usage|quota exceeded)`)

// IsUsageLimitText reports whether text reads like a usage-limit failure.
func IsUsageLimitText(text string) bool {
	return usageLimitPattern.MatchString(text)
}

// UsageLimitError marks a launch refused because the account is out of usage.
// Retrying on the same account fails the same way, so callers hold the runtime
// instead of failing the order or falling back to another runtime.
type UsageLimitError struct {
	Runtime string
	Detail  string
}

func (e UsageLimitError) Error() string {
	detail := strings.TrimSpace(e.Detail)
	if detail == "" {
		return fmt.Sprintf("%s usage limit reached", e.Runtime)
	}
	return fmt.Sprintf("%s usage limit reached: %s", e.Runtime, detail)
}

// IsUsageLimit reports whether err is, or wraps, a UsageLimitError.
func IsUsageLimit(err error) bool {
	var limitErr UsageLimitError
	return errors.As(err, &limitErr)
}
