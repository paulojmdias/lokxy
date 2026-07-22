package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/require"
)

// startWatch runs Watch in the background and returns a channel that receives
// one value per onChange invocation.
func startWatch(t *testing.T, path string) <-chan struct{} {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	changed := make(chan struct{}, 16)
	done := make(chan error, 1)
	go func() {
		done <- Watch(ctx, path, log.NewNopLogger(), func() {
			changed <- struct{}{}
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Watch did not return after context cancellation")
		}
	})

	// Give the watcher a moment to register the directory watch before the
	// test starts mutating files.
	time.Sleep(100 * time.Millisecond)
	return changed
}

func waitForChange(t *testing.T, changed <-chan struct{}) {
	t.Helper()
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not report a config change")
	}
}

func TestWatch_InPlaceWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a: 1\n"), 0o600))

	changed := startWatch(t, path)

	require.NoError(t, os.WriteFile(path, []byte("a: 2\n"), 0o600))
	waitForChange(t, changed)
}

func TestWatch_AtomicRename(t *testing.T) {
	// Simulates how Kubernetes updates ConfigMap mounts and how editors save:
	// write a new file, then rename it over the watched path (new inode).
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a: 1\n"), 0o600))

	changed := startWatch(t, path)

	tmp := filepath.Join(dir, "config.yaml.tmp")
	require.NoError(t, os.WriteFile(tmp, []byte("a: 2\n"), 0o600))
	require.NoError(t, os.Rename(tmp, path))
	waitForChange(t, changed)
}

func TestWatch_KubernetesDataSymlink(t *testing.T) {
	// The kubelet updates ConfigMap volumes by atomically swapping a ..data
	// symlink; the config file itself is a symlink chain through it, so no
	// event is ever emitted for the file's own name.
	dir := t.TempDir()

	v1 := filepath.Join(dir, "..v1")
	require.NoError(t, os.Mkdir(v1, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v1, "config.yaml"), []byte("a: 1\n"), 0o600))
	require.NoError(t, os.Symlink("..v1", filepath.Join(dir, "..data")))
	require.NoError(t, os.Symlink(filepath.Join("..data", "config.yaml"), filepath.Join(dir, "config.yaml")))

	changed := startWatch(t, filepath.Join(dir, "config.yaml"))

	v2 := filepath.Join(dir, "..v2")
	require.NoError(t, os.Mkdir(v2, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v2, "config.yaml"), []byte("a: 2\n"), 0o600))
	// Atomic swap: create the new symlink under a temp name, rename over ..data.
	require.NoError(t, os.Symlink("..v2", filepath.Join(dir, "..data_tmp")))
	require.NoError(t, os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")))
	waitForChange(t, changed)
}

func TestWatch_DebounceCollapsesBursts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a: 0\n"), 0o600))

	changed := startWatch(t, path)

	// A burst of writes well within the debounce window...
	for range 5 {
		require.NoError(t, os.WriteFile(path, []byte("a: 1\n"), 0o600))
		time.Sleep(10 * time.Millisecond)
	}
	waitForChange(t, changed)

	// ...must produce a single reload once the burst settles.
	select {
	case <-changed:
		t.Fatal("burst of writes triggered more than one reload")
	case <-time.After(2 * watchDebounce):
	}
}

func TestWatch_IgnoresUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("a: 1\n"), 0o600))

	changed := startWatch(t, path)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.yaml"), []byte("b: 1\n"), 0o600))

	select {
	case <-changed:
		t.Fatal("unrelated file change triggered a reload")
	case <-time.After(2 * watchDebounce):
	}
}
