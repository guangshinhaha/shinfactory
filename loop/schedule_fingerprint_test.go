package loop

import (
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/mise"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

func TestSchedulingFingerprintIgnoresEventsAndHistory(t *testing.T) {
	base := mise.Brief{Backlog: []adapter.BacklogItem{{ID: "1", Title: "scaffold"}}}
	fp := schedulingFingerprint(base)

	churned := base
	churned.RecentEvents = []mise.RecentEvent{{}}
	churned.RecentHistory = []mise.HistoryItem{{SessionID: "schedule-1", OrderID: scheduleOrderID, TaskKey: "schedule", Status: "completed"}}
	if got := schedulingFingerprint(churned); got != fp {
		t.Error("a schedule run's own events and history changed the fingerprint, so it would schedule again")
	}

	stageDone := base
	stageDone.RecentHistory = []mise.HistoryItem{{SessionID: "1-0", OrderID: "1", TaskKey: "execute", Status: "completed"}}
	if got := schedulingFingerprint(stageDone); got != fp {
		t.Error("a stage hand-off changed the fingerprint; only the backlog can unblock new work")
	}

	moreWork := base
	moreWork.Backlog = append(moreWork.Backlog, adapter.BacklogItem{ID: "2", Title: "manifest"})
	if got := schedulingFingerprint(moreWork); got == fp {
		t.Error("a backlog change did not change the fingerprint")
	}
}

// Some changes never reach the brief (an issue closed by hand), so with free
// capacity the loop schedules again once scheduleRefreshInterval has passed.
func TestScheduleRefreshesAfterIntervalWithFreeCapacity(t *testing.T) {
	clock := time.Date(2026, 11, 10, 23, 0, 0, 0, time.UTC)
	l := New(t.TempDir(), "noodle", localLandingConfig(), Dependencies{
		Runtimes: map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree: &fakeWorktree{},
		Adapter:  &fakeAdapterRunner{},
		Mise:     &fakeMise{},
		Monitor:  fakeMonitor{},
		Registry: testLoopRegistry(),
		Now:      func() time.Time { return clock },
	})
	brief := mise.Brief{Backlog: []adapter.BacklogItem{{ID: "5", Title: "api skeleton"}}}
	orders := OrdersFile{Orders: []Order{{ID: "43", Status: OrderStatusActive, Stages: []Stage{{TaskKey: "execute", Status: StageStatusActive}}}}}
	l.lastScheduledFingerprint = schedulingFingerprint(brief)
	l.lastScheduledAt = clock

	clock = clock.Add(time.Minute)
	if _, err := l.ensureScheduleIfNeeded(brief, &orders, false); err != nil {
		t.Fatal(err)
	}
	if hasScheduleOrder(orders) {
		t.Fatal("scheduled again a minute later with nothing changed")
	}

	clock = clock.Add(scheduleRefreshInterval)
	if _, err := l.ensureScheduleIfNeeded(brief, &orders, false); err != nil {
		t.Fatal(err)
	}
	if !hasScheduleOrder(orders) {
		t.Fatal("no schedule after the refresh interval with free capacity")
	}
}
