// Package watch reports changes to a single file using inotify (via fsnotify).
package watch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// kubernetesDataLink is the symlink Kubernetes replaces atomically when a
// ConfigMap volume is updated; files in the mount are symlinks into it.
const kubernetesDataLink = "..data"

// Watch calls onChange (debounced) whenever the file at path is created, written, renamed over or removed+recreated. Blocks until ctx is cancelled.
func Watch(ctx context.Context, path string, debounce time.Duration, onChange func()) error {
	return run(ctx, path, debounce, onChange, nil)
}

// run is Watch with an optional ready hook, called once the directory is being watched.
func run(ctx context.Context, path string, debounce time.Duration, onChange func(), ready func()) error {
	path = filepath.Clean(path)
	dir, name := filepath.Dir(path), filepath.Base(path)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()
	if err := w.Add(dir); err != nil {
		return fmt.Errorf("watch directory %s: %w", dir, err)
	}
	if ready != nil {
		ready()
	}

	var fire <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return errors.New("watcher stopped unexpectedly")
			}
			if !relevant(ev, name) {
				slog.Debug("ignoring directory event", "event", ev.String())
				continue
			}
			slog.Debug("config file event", "event", ev.String())
			fire = time.After(debounce)
		case <-fire:
			fire = nil
			onChange()
		case err, ok := <-w.Errors:
			if !ok {
				return errors.New("watcher stopped unexpectedly")
			}
			slog.Warn("config watcher error", "err", err)
		}
	}
}

func relevant(ev fsnotify.Event, name string) bool {
	base := filepath.Base(ev.Name)
	if base != name && base != kubernetesDataLink {
		return false
	}
	return ev.Has(fsnotify.Create) || ev.Has(fsnotify.Write) || ev.Has(fsnotify.Rename) || ev.Has(fsnotify.Remove)
}
