package rootlock

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

var ErrPersistenceRootBusy = errors.New("persistence root is already owned by another app-server process")

type Lease struct {
	path string
	lock *flock.Flock
}

func Acquire(persistenceRoot string) (*Lease, error) {
	root := strings.TrimSpace(persistenceRoot)
	if root == "" {
		return nil, errors.New("persistence root is required")
	}
	path := filepath.Join(root, "app-server.lock")
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire app-server root lock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("%w: %s", ErrPersistenceRootBusy, path)
	}
	return &Lease{path: path, lock: lock}, nil
}

func (l *Lease) Close() error {
	if l == nil || l.lock == nil {
		return nil
	}
	if err := l.lock.Unlock(); err != nil {
		return fmt.Errorf("release app-server root lock %s: %w", l.path, err)
	}
	return nil
}
