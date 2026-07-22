package config

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

// watchDebounce collapses the bursts of filesystem events emitted by editors
// and by the kubelet when it swaps a ConfigMap volume into a single reload.
const watchDebounce = 500 * time.Millisecond

// kubernetesDataLink is the symlink the kubelet atomically swaps when a
// ConfigMap or Secret volume is updated.
const kubernetesDataLink = "..data"

// Watch monitors the configuration file and invokes onChange after it is
// created, written, or replaced. It blocks until ctx is canceled.
//
// The parent directory is watched rather than the file itself so watching
// survives atomic replacement, as done by the kubelet for ConfigMap mounts
// (the ..data symlink swap changes the file's inode) and by editors that
// write-and-rename.
func Watch(ctx context.Context, path string, logger log.Logger, onChange func()) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve config path %q: %w", path, err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create config watcher: %w", err)
	}
	defer watcher.Close()

	dir := filepath.Dir(absPath)
	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("failed to watch config directory %q: %w", dir, err)
	}
	level.Info(logger).Log("msg", "Watching configuration file for changes", "path", absPath)

	debounce := time.NewTimer(watchDebounce)
	debounce.Stop()
	defer debounce.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if isConfigEvent(event, absPath) {
				debounce.Reset(watchDebounce)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			level.Error(logger).Log("msg", "Config watcher error", "err", err)
		case <-debounce.C:
			level.Info(logger).Log("msg", "Configuration file changed on disk", "path", absPath)
			onChange()
		}
	}
}

// isConfigEvent reports whether a filesystem event affects the watched
// configuration file: a create/write/rename of the file itself or of the
// Kubernetes ..data symlink that backs ConfigMap and Secret mounts.
func isConfigEvent(event fsnotify.Event, absPath string) bool {
	if !event.Op.Has(fsnotify.Create) && !event.Op.Has(fsnotify.Write) && !event.Op.Has(fsnotify.Rename) {
		return false
	}
	name := filepath.Clean(event.Name)
	return name == absPath || filepath.Base(name) == kubernetesDataLink
}
