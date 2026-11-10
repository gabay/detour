package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	debounce = 100 * time.Millisecond
	deadline = 3 * time.Second
	quiet    = 400 * time.Millisecond
)

type harness struct {
	t       *testing.T
	dir     string
	path    string
	changes chan struct{}
	done    chan error
	cancel  context.CancelFunc
}

func start(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), changes: make(chan struct{}, 64), done: make(chan error, 1)}
	h.path = filepath.Join(h.dir, "detour.yaml")
	if err := os.WriteFile(h.path, []byte("v0"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	var ctx context.Context
	ctx, h.cancel = context.WithCancel(context.Background())
	go func() {
		h.done <- run(ctx, h.path, debounce, func() { h.changes <- struct{}{} }, func() { close(ready) })
	}()
	select {
	case <-ready:
	case <-time.After(deadline):
		t.Fatal("watcher not ready")
	}
	t.Cleanup(func() { h.stop() })
	return h
}

func (h *harness) stop() {
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Errorf("Watch returned %v after cancel", err)
		}
	case <-time.After(deadline):
		h.t.Error("Watch did not return after cancel")
	}
}

func (h *harness) waitChange() {
	h.t.Helper()
	select {
	case <-h.changes:
	case <-time.After(deadline):
		h.t.Fatal("no callback")
	}
}

// assertQuiet fails if a callback fires within the quiet window.
func (h *harness) assertQuiet() {
	h.t.Helper()
	select {
	case <-h.changes:
		h.t.Fatal("unexpected callback")
	case <-time.After(quiet):
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTriggersOnce(t *testing.T) {
	h := start(t)
	write(t, h.path, "v1")
	h.waitChange()
	h.assertQuiet()
}

func TestBurstIsDebounced(t *testing.T) {
	h := start(t)
	for i := range 10 {
		write(t, h.path, string(rune('a'+i)))
	}
	h.waitChange()
	h.assertQuiet()
}

func TestAtomicRenameReplace(t *testing.T) {
	h := start(t)
	tmp := filepath.Join(h.dir, ".detour.yaml.tmp")
	write(t, tmp, "v1")
	if err := os.Rename(tmp, h.path); err != nil {
		t.Fatal(err)
	}
	h.waitChange()
	h.assertQuiet()
}

func TestRemoveAndRecreate(t *testing.T) {
	h := start(t)
	if err := os.Remove(h.path); err != nil {
		t.Fatal(err)
	}
	h.waitChange()
	write(t, h.path, "v2")
	h.waitChange()
}

func TestUnrelatedFileIgnored(t *testing.T) {
	h := start(t)
	write(t, filepath.Join(h.dir, "other.yaml"), "x")
	h.assertQuiet()
}

func TestKubernetesDataSymlinkSwap(t *testing.T) {
	h := start(t)
	// Lay out a ConfigMap volume: detour.yaml -> ..data/detour.yaml, ..data -> <timestamped dir>.
	v1 := filepath.Join(h.dir, "..2026_01_01")
	if err := os.Mkdir(v1, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(v1, "detour.yaml"), "v1")
	if err := os.Symlink("..2026_01_01", filepath.Join(h.dir, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "detour.yaml"), h.path); err != nil {
		t.Fatal(err)
	}
	h.waitChange()

	// Kubernetes swaps ..data atomically: new symlink, then rename over ..data.
	v2 := filepath.Join(h.dir, "..2026_02_01")
	if err := os.Mkdir(v2, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(v2, "detour.yaml"), "v2")
	tmpLink := filepath.Join(h.dir, "..data_tmp")
	if err := os.Symlink("..2026_02_01", tmpLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpLink, filepath.Join(h.dir, "..data")); err != nil {
		t.Fatal(err)
	}
	h.waitChange()
}

func TestMissingDirectoryReturnsError(t *testing.T) {
	err := Watch(context.Background(), filepath.Join(t.TempDir(), "missing", "detour.yaml"), debounce, func() {})
	if err == nil {
		t.Fatal("want error for missing directory")
	}
}
