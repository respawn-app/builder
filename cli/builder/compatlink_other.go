//go:build !windows

package main

import "os"

// createCompatLink creates the compat link old -> new. On Unix this is an
// ordinary symlink.
func createCompatLink(target string, link string) error {
	return os.Symlink(target, link)
}

// isCompatLink reports whether fi (from os.Lstat of the old root) describes a
// compat link rather than a real directory. On Unix that is a symlink.
func isCompatLink(fi os.FileInfo) bool {
	return fi.Mode()&os.ModeSymlink != 0
}
