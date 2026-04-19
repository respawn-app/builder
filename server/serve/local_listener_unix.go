//go:build unix

package serve

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"builder/shared/config"
)

func listenLocalSocket(cfg config.App) (net.Listener, func(), bool, error) {
	socketPath, ok, err := config.ServerLocalRPCSocketPath(cfg)
	if err != nil || !ok {
		return nil, nil, ok, err
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, nil, false, fmt.Errorf("create local unix socket dir: %w", err)
	}
	if err := removeStaleSocket(socketPath); err != nil {
		return nil, nil, false, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, nil, false, fmt.Errorf("listen local unix control endpoint: %w", err)
	}
	cleanup := func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}
	return listener, cleanup, true, nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat local unix socket path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("local unix socket path exists and is not a socket: %s", path)
	}
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("local unix socket already active: %s", path)
	}
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("remove stale local unix socket: %w", removeErr)
	}
	return nil
}
