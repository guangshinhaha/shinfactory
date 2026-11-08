package rtcap

import (
	"sync"

	"github.com/guangshinhaha/shinfactory/internal/stringx"
)

// Default capability profiles for known runtimes.
var (
	ProcessCaps = RuntimeCapabilities{
		Name: "process",
		Caps: map[Capability]bool{
			CapSteerable:  true,
			CapPolling:    false,
			CapRemoteSync: false,
			CapHeartbeat:  false,
		},
	}

	SpritesCaps = RuntimeCapabilities{
		Name: "sprites",
		Caps: map[Capability]bool{
			CapSteerable:  true,
			CapPolling:    false,
			CapRemoteSync: false,
			CapHeartbeat:  false,
		},
	}

	CursorCaps = RuntimeCapabilities{
		Name: "cursor",
		Caps: map[Capability]bool{
			CapSteerable:  false,
			CapPolling:    true,
			CapRemoteSync: true,
			CapHeartbeat:  false,
		},
	}

	// OrcaCaps: an Orca worker is followed by polling and cannot be steered
	// mid-run through the factory.
	OrcaCaps = RuntimeCapabilities{
		Name: "orca",
		Caps: map[Capability]bool{
			CapSteerable:  false,
			CapPolling:    true,
			CapRemoteSync: false,
			CapHeartbeat:  true,
		},
	}
)

// RuntimeRegistry maps runtime names to their capabilities.
type RuntimeRegistry struct {
	mu       sync.RWMutex
	runtimes map[string]RuntimeCapabilities
}

// NewRegistry returns a RuntimeRegistry pre-loaded with the default profiles.
func NewRegistry() *RuntimeRegistry {
	r := &RuntimeRegistry{
		runtimes: make(map[string]RuntimeCapabilities),
	}
	r.runtimes[ProcessCaps.Name] = ProcessCaps
	r.runtimes[SpritesCaps.Name] = SpritesCaps
	r.runtimes[CursorCaps.Name] = CursorCaps
	r.runtimes[OrcaCaps.Name] = OrcaCaps
	return r
}

// Register adds or replaces a runtime's capability profile.
func (r *RuntimeRegistry) Register(name string, caps RuntimeCapabilities) {
	name = stringx.Normalize(name)
	r.mu.Lock()
	defer r.mu.Unlock()
	caps.Name = name
	r.runtimes[name] = caps
}

// Get returns the capabilities for a named runtime.
func (r *RuntimeRegistry) Get(name string) (RuntimeCapabilities, bool) {
	name = stringx.Normalize(name)
	r.mu.RLock()
	defer r.mu.RUnlock()
	caps, ok := r.runtimes[name]
	return caps, ok
}

// DefaultCapabilities returns the process runtime capabilities as default.
func (r *RuntimeRegistry) DefaultCapabilities() RuntimeCapabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if caps, ok := r.runtimes["process"]; ok {
		return caps
	}
	return ProcessCaps
}
