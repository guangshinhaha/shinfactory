package dispatcher

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
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

var (
	resetClockPattern = regexp.MustCompile(`(?i)\bresets?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\b(?:\s*\(([A-Za-z_]+(?:/[A-Za-z_+\-0-9]+)*)\))?`)
	resetInPattern    = regexp.MustCompile(`(?i)\bresets?\s+in\s+((?:\d+\s*(?:hours?|hrs?|h|minutes?|mins?|m)\b[\s,]*(?:and\s+)?)+)`)
	resetUnitPattern  = regexp.MustCompile(`(?i)(\d+)\s*(hours?|hrs?|h|minutes?|mins?|m)\b`)
	resetEpochPattern = regexp.MustCompile(`(?i)limit reached\|(\d{10})\b`)
)

// maxUsageLimitWait bounds a parsed reset time. A reading further out than a
// weekly window is a misparse, so it is ignored and the caller falls back to
// retrying.
const maxUsageLimitWait = 8 * 24 * time.Hour

// ParseUsageLimitReset reads when a usage limit resets out of the error text
// ("resets at 3pm (Asia/Singapore)", "resets in 2 hours 30 minutes", or the
// "limit reached|<unix time>" form). It returns false when the text names no
// usable time in the future, so callers can fall back to retrying.
func ParseUsageLimitReset(text string, now time.Time) (time.Time, bool) {
	var reset time.Time
	switch {
	case resetEpochPattern.MatchString(text):
		secs, _ := strconv.ParseInt(resetEpochPattern.FindStringSubmatch(text)[1], 10, 64)
		reset = time.Unix(secs, 0)
	case resetInPattern.MatchString(text):
		var wait time.Duration
		for _, m := range resetUnitPattern.FindAllStringSubmatch(resetInPattern.FindStringSubmatch(text)[1], -1) {
			n, _ := strconv.Atoi(m[1])
			if strings.HasPrefix(strings.ToLower(m[2]), "h") {
				wait += time.Duration(n) * time.Hour
			} else {
				wait += time.Duration(n) * time.Minute
			}
		}
		reset = now.Add(wait)
	case resetClockPattern.MatchString(text):
		m := resetClockPattern.FindStringSubmatch(text)
		hour, _ := strconv.Atoi(m[1])
		minute, _ := strconv.Atoi(m[2])
		if hour < 1 || hour > 12 || minute > 59 {
			return time.Time{}, false
		}
		hour %= 12
		if strings.EqualFold(m[3], "pm") {
			hour += 12
		}
		loc := now.Location()
		if m[4] != "" {
			if named, err := time.LoadLocation(m[4]); err == nil {
				loc = named
			}
		}
		local := now.In(loc)
		reset = time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
		if !reset.After(now) {
			reset = reset.AddDate(0, 0, 1)
		}
	default:
		return time.Time{}, false
	}
	if !reset.After(now) || reset.Sub(now) > maxUsageLimitWait {
		return time.Time{}, false
	}
	return reset, true
}
