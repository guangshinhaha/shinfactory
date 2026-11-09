package runtime

import (
	"context"
	"log/slog"

	"github.com/guangshinhaha/shinfactory/dispatcher"
)

// orcaRuntime adds restart recovery to the base DispatcherRuntime. A restarted
// loop cannot collect the result of an Orca worker its previous process
// launched, so recovery stops those workers; their stages go back to pending
// and relaunch in the kept worktrees.
type orcaRuntime struct {
	*DispatcherRuntime
	orca *dispatcher.OrcaDispatcher
}

// NewOrcaRuntime adapts an orca dispatcher to the Runtime interface.
func NewOrcaRuntime(d *dispatcher.OrcaDispatcher, runtimeDir string, maxConcurrent int) Runtime {
	r := NewDispatcherRuntime("orca", d, runtimeDir)
	r.SetMaxConcurrent(maxConcurrent)
	return &orcaRuntime{DispatcherRuntime: r, orca: d}
}

func (o *orcaRuntime) Recover(ctx context.Context) ([]RecoveredSession, error) {
	stopped, err := o.orca.RecoverOrphans(ctx)
	if len(stopped) > 0 {
		slog.Warn("stopped orca workers orphaned by a loop restart", "dispatches", stopped)
	}
	return nil, err
}
