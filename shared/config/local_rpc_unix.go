//go:build unix

package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const localRPCSocketFilename = "rpc.sock"

func ServerLocalRPCSocketPath(cfg App) (string, bool, error) {
	trimmedRoot := strings.TrimSpace(cfg.PersistenceRoot)
	if trimmedRoot == "" {
		return "", false, nil
	}
	runtimeBase := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR"))
	if runtimeBase == "" {
		runtimeBase = filepath.Join(os.TempDir(), DefaultAppName+"-"+strconv.Itoa(os.Getuid()))
	}
	hash := sha256.Sum256([]byte(filepath.Clean(trimmedRoot)))
	rootHash := hex.EncodeToString(hash[:8])
	return filepath.Join(runtimeBase, DefaultAppName, "rpc", rootHash, localRPCSocketFilename), true, nil
}
