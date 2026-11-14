package filex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicCreatesParentAndFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a", "b", "payload.json")
	if err := WriteFileAtomic(path, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("write atomic file: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("file contents mismatch: got %q", string(got))
	}
}

func TestWriteFileAtomicReplacesExistingContents(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "orders.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteFileAtomic(path, []byte("new")); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten file: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("file contents mismatch: got %q", string(got))
	}
}

func TestWriteFileAtomicIfChangedSkipsIdenticalContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	if err := WriteFileAtomicIfChanged(path, []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomicIfChanged(path, []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("identical content replaced the file")
	}
	if err := WriteFileAtomicIfChanged(path, []byte("b\n")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "b\n" {
		t.Fatalf("content = %q, want b", got)
	}
}
