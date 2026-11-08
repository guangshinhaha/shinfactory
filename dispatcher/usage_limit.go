package dispatcher

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// usageLimitPattern matches the wording agents and Orca use when the account
// behind a launch has no usage left. It stays narrow on purpose: an ordinary
// failure that merely mentions a "rate limit" (a test name, a code review) must
// not hold the runtime, so bare "rate limit" and "limit reached" don't match.
var usageLimitPattern = regexp.MustCompile(
	`(?i)(usage limit reached|(5-hour|five-hour|weekly|session|opus|sonnet) limit reached|hit your (usage |weekly |5-hour |session )?limit|reached your (usage|weekly|5-hour|session) limit|out of (extra )?(usage|credits)|credit balance is too low|exceeded your (usage|quota)|insufficient credits|rate_limit_error)`)

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
