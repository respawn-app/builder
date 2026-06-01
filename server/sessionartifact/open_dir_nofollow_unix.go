//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package sessionartifact

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func openDirectoryNoSymlink(root *os.Root, rel string) (*os.File, error) {
	dir, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := dir.Stat()
	if err != nil {
		_ = dir.Close()
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		_ = dir.Close()
		return nil, fmt.Errorf("%q must not be a symlink", rel)
	}
	if !info.IsDir() {
		_ = dir.Close()
		return nil, fmt.Errorf("%q must be a directory", rel)
	}
	return dir, nil
}
