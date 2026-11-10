package loop

import (
	"testing"

	"github.com/guangshinhaha/shinfactory/adapter"
	"github.com/guangshinhaha/shinfactory/mise"
)

func TestSchedulingFingerprintIgnoresScheduleChurn(t *testing.T) {
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
	if got := schedulingFingerprint(stageDone); got == fp {
		t.Error("a finished build stage did not change the fingerprint")
	}

	moreWork := base
	moreWork.Backlog = append(moreWork.Backlog, adapter.BacklogItem{ID: "2", Title: "manifest"})
	if got := schedulingFingerprint(moreWork); got == fp {
		t.Error("a backlog change did not change the fingerprint")
	}
}
