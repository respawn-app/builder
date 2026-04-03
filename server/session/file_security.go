package session

import (
	"fmt"
	"os"
)

func readRegularSessionFile(path string, label string) ([]byte, error) {
	if err := ensureRegularSessionFile(path, label); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func openRegularSessionFile(path string, label string) (*os.File, error) {
	if err := ensureRegularSessionFile(path, label); err != nil {
		return nil, err
	}
	fp, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return fp, nil
}

func ensureRegularSessionFile(path string, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", label)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", label)
	}
	return nil
}
