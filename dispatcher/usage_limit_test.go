package dispatcher

import (
	"fmt"
	"testing"
	"time"
)

func TestParseUsageLimitReset(t *testing.T) {
	sgt, _ := time.LoadLocation("Asia/Singapore")
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC) // 18:00 in Singapore
	cases := []struct {
		name string
		text string
		want time.Time
		ok   bool
	}{
		{"clock in named zone", "You've hit your limit · resets 3pm (Asia/Singapore)", time.Date(2026, 11, 10, 15, 0, 0, 0, sgt), true},
		{"clock later today", "Your limit will reset at 11:30pm (UTC).", time.Date(2026, 11, 9, 23, 30, 0, 0, time.UTC), true},
		{"clock already passed rolls to tomorrow", "limit resets at 9am (UTC)", time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC), true},
		{"midnight", "resets 12am (UTC)", time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), true},
		{"noon", "resets 12pm (UTC)", time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC), true},
		{"relative", "usage limit reached, resets in 2 hours 30 minutes", now.Add(150 * time.Minute), true},
		{"relative minutes", "resets in 45 minutes", now.Add(45 * time.Minute), true},
		{"epoch", fmt.Sprintf("Claude AI usage limit reached|%d", now.Add(3*time.Hour).Unix()), now.Add(3 * time.Hour), true},
		{"no time", "Claude usage limit reached.", time.Time{}, false},
		{"epoch in the past", "limit reached|1700000000", time.Time{}, false},
		{"too far away", "resets in 400 hours", time.Time{}, false},
		{"impossible hour", "resets at 13pm", time.Time{}, false},
	}
	for _, c := range cases {
		got, ok := ParseUsageLimitReset(c.text, now)
		if ok != c.ok || (ok && !got.Equal(c.want)) {
			t.Errorf("%s: ParseUsageLimitReset(%q) = %v, %v; want %v, %v", c.name, c.text, got, ok, c.want, c.ok)
		}
	}
}

func TestIsUsageLimitTextIgnoresBareRateLimitError(t *testing.T) {
	if IsUsageLimitText(`handler returns {"type":"rate_limit_error"} on 429`) {
		t.Fatal("a failure that merely mentions rate_limit_error read as a usage limit")
	}
	if !IsUsageLimitText("Claude usage limit reached. resets 3pm") {
		t.Fatal("a real usage limit message no longer matches")
	}
}
