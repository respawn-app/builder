package primaryrun

import "errors"

var ErrActivePrimaryRun = errors.New("session already has an active primary run")

type Lease interface {
	Release()
}

type LeaseFunc func()

func (fn LeaseFunc) Release() {
	if fn != nil {
		fn()
	}
}

type Gate interface {
	AcquirePrimaryRun(sessionID string) (Lease, error)
}
