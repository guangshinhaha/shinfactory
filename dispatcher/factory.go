package dispatcher

import (
	"context"
	"fmt"
	"os"
)

// DispatcherFactory routes dispatch requests to a runtime-specific dispatcher.
type DispatcherFactory struct {
	runtimes map[string]Dispatcher
}

func NewDispatcherFactory() *DispatcherFactory {
	return &DispatcherFactory{
		runtimes: map[string]Dispatcher{},
	}
}

func (f *DispatcherFactory) Register(runtime string, dispatcher Dispatcher) error {
	if f == nil {
		return fmt.Errorf("dispatcher factory not initialized")
	}
	if dispatcher == nil {
		return fmt.Errorf("dispatcher for runtime %q not configured", NormalizeRuntime(runtime))
	}
	key := NormalizeRuntime(runtime)
	f.runtimes[key] = dispatcher
	return nil
}

func (f *DispatcherFactory) Dispatch(ctx context.Context, req DispatchRequest) (Session, error) {
	if f == nil {
		return nil, fmt.Errorf("dispatcher factory not initialized")
	}
	runtime := NormalizeRuntime(req.Runtime)
	dispatcher, ok := f.runtimes[runtime]
	if !ok {
		return nil, fmt.Errorf("runtime %q not configured", runtime)
	}
	req.Runtime = runtime
	session, err := dispatcher.Dispatch(ctx, req)
	// An orca stage never falls back: process runs on the default login, not the
	// Orca-managed one, and nothing would say so. The caller holds or fails the stage.
	if err != nil && AllowsProcessFallback(runtime) && !IsUsageLimit(err) {
		fallback, hasFallback := f.runtimes["process"]
		if hasFallback {
			fmt.Fprintf(os.Stderr, "dispatch: %s runtime failed, falling back to process: %v\n", runtime, err)
			req.Runtime = "process"
			req.DispatchWarning = fmt.Sprintf("%s dispatch failed: %v", runtime, err)
			return fallback.Dispatch(ctx, req)
		}
	}
	return session, err
}

var _ Dispatcher = (*DispatcherFactory)(nil)

// AllowsProcessFallback reports whether a failed launch on runtime may be
// retried as a plain process. Orca runs on the Orca-managed login; process
// would use the default one, so an orca stage fails or holds instead.
func AllowsProcessFallback(runtime string) bool {
	r := NormalizeRuntime(runtime)
	return r != "process" && r != "orca"
}
