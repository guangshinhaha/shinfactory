package loop

import (
	"github.com/guangshinhaha/shinfactory/internal/linestatus"
	"github.com/guangshinhaha/shinfactory/internal/loopreg"
	"slices"
	"sort"

	"github.com/guangshinhaha/shinfactory/internal/statusfile"
)

// stampStatus writes active IDs, loop state, and mode into status.json
// so the TUI can derive cooking status and display the current run mode.
// Skips the write when nothing changed, avoiding fs-watcher feedback loops.
func (l *Loop) stampStatus() error {
	active := make([]string, 0, len(l.cooks.activeCooksByOrder))
	for targetID := range l.cooks.activeCooksByOrder {
		active = append(active, targetID)
	}
	sort.Strings(active)

	// Read mode from canonical state (V2 source of truth).
	mode := string(l.canonical.Mode)
	if mode == "" {
		mode = l.config.Mode
	}

	status := statusfile.Status{
		Active:         active,
		LoopState:      string(l.state),
		Mode:           mode,
		MaxConcurrency: l.config.Concurrency.MaxConcurrency,
		Warnings:       l.lastMiseWarnings,
	}

	l.announceState(status)

	// Skip write if nothing changed.
	if slices.Equal(l.lastStatus.Active, status.Active) &&
		l.lastStatus.LoopState == status.LoopState &&
		l.lastStatus.Mode == status.Mode &&
		l.lastStatus.MaxConcurrency == status.MaxConcurrency &&
		slices.Equal(l.lastStatus.Warnings, status.Warnings) {
		l.publishState()
		return nil
	}

	if err := statusfile.WriteAtomic(l.deps.StatusFile, status); err != nil {
		return err
	}
	l.lastStatus = status
	l.publishState()
	return nil
}

// Announce registers this loop in the machine-wide registry at dir, so
// `shinfactory ps` and other dashboards can find it. The file is written on the
// next status stamp and removed by Shutdown. Call it before Run.
func (l *Loop) Announce(dir, version string) {
	l.announcer = loopreg.NewRegistrar(dir, l.projectDir, l.deps.Now)
	l.announceVersion = version
}

// SetPort tells the registry which port the dashboard listens on.
func (l *Loop) SetPort(port int) {
	if l.announcer == nil {
		return
	}
	if err := l.announcer.SetPort(port); err != nil {
		l.logger.Warn("could not update loop registry port", "error", err)
	}
}

// announceState publishes the loop's current state to the registry. A failed
// write is logged: the registry is a convenience and must not stop the loop.
func (l *Loop) announceState(status statusfile.Status) {
	if l.announcer == nil {
		return
	}
	needsYou, err := linestatus.NeedsShinCount(l.runtimeDir)
	if err != nil {
		l.logger.Warn("could not count needs-shin for loop registry", "error", err)
	}
	err = l.announcer.Publish(loopreg.Entry{
		ProjectDir: l.projectDir,
		LoopState:  status.LoopState,
		Mode:       status.Mode,
		SlotsBusy:  len(status.Active),
		SlotsMax:   status.MaxConcurrency,
		NeedsYou:   needsYou,
		Version:    l.announceVersion,
	})
	if err != nil {
		l.logger.Warn("could not write loop registry entry", "error", err)
	}
}
