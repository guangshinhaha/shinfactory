package loop

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/statusfile"
	"github.com/guangshinhaha/shinfactory/monitor"
	loopruntime "github.com/guangshinhaha/shinfactory/runtime"
)

type idleMonitor struct{ metas []monitor.SessionMeta }

func (m *idleMonitor) RunOnce(context.Context) ([]monitor.SessionMeta, error) { return m.metas, nil }

// A session with no event or log output for longer than [monitor]
// stuck_after_minutes is reported in status.json and warned about once (#205).
func TestStuckSessionReportedInStatusAndLoggedOnce(t *testing.T) {
	projectDir := t.TempDir()
	statusPath := filepath.Join(projectDir, ".noodle", "status.json")
	var logs bytes.Buffer
	mon := &idleMonitor{metas: []monitor.SessionMeta{
		{SessionID: "s-quiet", Status: monitor.SessionStatusRunning, Alive: true, IdleSeconds: 45 * 60},
		{SessionID: "s-busy", Status: monitor.SessionStatusRunning, Alive: true, IdleSeconds: 30},
	}}
	cfg := localLandingConfig()
	cfg.Monitor.StuckAfterMinutes = 20
	l := New(projectDir, "noodle", cfg, Dependencies{
		Runtimes:   map[string]loopruntime.Runtime{"process": newMockRuntime()},
		Worktree:   &fakeWorktree{},
		Adapter:    &fakeAdapterRunner{},
		Mise:       &fakeMise{},
		Monitor:    mon,
		Registry:   testLoopRegistry(),
		Now:        time.Now,
		OrdersFile: filepath.Join(projectDir, ".noodle", "orders.json"),
		StatusFile: statusPath,
		Logger:     slog.New(slog.NewTextHandler(&logs, nil)),
	})
	for orderID, sid := range map[string]string{"38": "s-quiet", "39": "s-busy"} {
		l.cooks.activeCooksByOrder[orderID] = &cookHandle{
			cookIdentity: cookIdentity{orderID: orderID, stage: Stage{TaskKey: "execute", Status: StageStatusActive}},
			session:      &mockSession{id: sid, status: "running", done: make(chan struct{})},
		}
	}

	l.runMonitorPass(context.Background())
	l.runMonitorPass(context.Background())
	if err := l.stampStatus(); err != nil {
		t.Fatalf("stampStatus: %v", err)
	}

	status, err := statusfile.Read(statusPath)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !slices.Equal(status.Stuck, []string{"38"}) {
		t.Fatalf("status.Stuck = %v, want [38]", status.Stuck)
	}
	if n := strings.Count(logs.String(), "stage looks stuck"); n != 1 {
		t.Fatalf("stuck warning logged %d times over two passes, want 1:\n%s", n, logs.String())
	}
}
