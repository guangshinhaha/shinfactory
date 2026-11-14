// Package loopreg is the machine-wide registry of running loops. Each loop
// writes one JSON file under ~/.local/state/shinfactory/loops so tools can list
// every factory line on the machine without anyone remembering ports.
package loopreg

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guangshinhaha/shinfactory/internal/filex"
	"github.com/guangshinhaha/shinfactory/internal/procx"
)

// Entry is what one loop announces about itself.
type Entry struct {
	Repo       string    `json:"repo"`
	ProjectDir string    `json:"project_dir"`
	Port       int       `json:"port"`
	PID        int       `json:"pid"`
	LoopState  string    `json:"loop_state"`
	Mode       string    `json:"mode"`
	SlotsBusy  int       `json:"slots_busy"`
	SlotsMax   int       `json:"slots_max"`
	NeedsYou   int       `json:"needs_you"`
	Version    string    `json:"version"`
	UpdatedAt  time.Time `json:"updated_at"`
	// Stale is set by List, never written: the file's pid is no longer alive.
	Stale bool `json:"stale"`
}

// DefaultDir is where loops register: ~/.local/state/shinfactory/loops.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for the loop registry: %w", err)
	}
	return filepath.Join(home, ".local", "state", "shinfactory", "loops"), nil
}

// List reads every registered loop, marks those whose pid is dead as stale and
// sorts by repo then project dir. A missing directory is an empty registry. A
// file that does not parse is skipped: one torn file must not hide the rest.
func List(dir string) ([]Entry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Entry{}, nil
		}
		return nil, fmt.Errorf("read loop registry %s: %w", dir, err)
	}
	entries := []Entry{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var e Entry
		if err := json.Unmarshal(data, &e); err != nil {
			continue
		}
		e.Stale = !procx.IsPIDAlive(e.PID)
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Repo != entries[j].Repo {
			return entries[i].Repo < entries[j].Repo
		}
		return entries[i].ProjectDir < entries[j].ProjectDir
	})
	return entries, nil
}

// Registrar owns one loop's registry file: it writes on change and removes the
// file on clean exit. Safe for concurrent use; the loop goroutine publishes
// while the web server goroutine reports the port.
type Registrar struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	port    int
	last    Entry
	written bool
	removed bool
}

// NewRegistrar picks the file for projectDir. The file is <repo>.json; when
// another live loop on a different project already uses that name, the name
// gains a short hash of the project dir so neither overwrites the other.
func NewRegistrar(dir, projectDir string, now func() time.Time) *Registrar {
	if now == nil {
		now = time.Now
	}
	repo := RepoName(projectDir)
	path := filepath.Join(dir, repo+".json")
	if data, err := os.ReadFile(path); err == nil {
		var other Entry
		if json.Unmarshal(data, &other) == nil && other.ProjectDir != projectDir && procx.IsPIDAlive(other.PID) {
			sum := sha256.Sum256([]byte(projectDir))
			path = filepath.Join(dir, repo+"-"+hex.EncodeToString(sum[:])[:8]+".json")
		}
	}
	return &Registrar{path: path, now: now}
}

// RepoName is the registry name for a project directory.
func RepoName(projectDir string) string {
	name := filepath.Base(filepath.Clean(projectDir))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "repo"
	}
	return name
}

// SetPort records the dashboard port and rewrites the file if one is out.
func (r *Registrar) SetPort(port int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.port = port
	if !r.written || r.removed {
		return nil
	}
	return r.writeLocked(r.last)
}

// Publish writes e, skipping the write when nothing but the timestamp changed.
// Repo, pid and port are filled in here.
func (r *Registrar) Publish(e Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.removed {
		return nil
	}
	e.Repo = RepoName(e.ProjectDir)
	e.PID = os.Getpid()
	e.Port = r.port
	if r.written && sameState(r.last, e) {
		return nil
	}
	return r.writeLocked(e)
}

func (r *Registrar) writeLocked(e Entry) error {
	e.Port = r.port
	e.UpdatedAt = r.now().UTC()
	e.Stale = false
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("encode loop registry entry: %w", err)
	}
	if err := filex.WriteFileAtomic(r.path, append(data, '\n')); err != nil {
		return fmt.Errorf("write loop registry entry: %w", err)
	}
	r.last = e
	r.written = true
	return nil
}

// Remove deletes the file. After it, Publish and SetPort write nothing, so a
// late cycle cannot resurrect the entry of a loop that has exited.
func (r *Registrar) Remove() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = true
	if err := os.Remove(r.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove loop registry entry: %w", err)
	}
	return nil
}

func sameState(a, b Entry) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	return a == b
}
