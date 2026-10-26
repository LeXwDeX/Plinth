package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
)

// Catalog and runtime dependencies are constructed before this state is exposed.
// Workers turn their flags off before reporting termination to the run loop.
type readinessState struct {
	coordinator atomic.Bool
	chat        atomic.Bool
	ping        func(context.Context) error
	storageRoot string
}

func (r *readinessState) check(ctx context.Context) error {
	if !r.coordinator.Load() || !r.chat.Load() {
		return errors.New("background services are not running")
	}
	if err := r.ping(ctx); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Join(r.storageRoot, "tmp"), "readiness-*")
	if err != nil {
		return err
	}
	name := file.Name()
	_, writeErr := file.Write([]byte{0})
	syncErr := file.Sync()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	return errors.Join(writeErr, syncErr, closeErr, removeErr)
}
