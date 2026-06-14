//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCreateCompatLinkJunctionWindows verifies the compat link on Windows is a
// working directory junction (no elevation required) and that isCompatLink tells
// it apart from a real directory. Runs only on the windows builder.
func TestCreateCompatLinkJunctionWindows(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "kent")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(base, "builder")
	if err := createCompatLink(target, link); err != nil {
		t.Fatalf("createCompatLink: %v", err)
	}

	// The junction must resolve into the target directory's contents.
	if _, err := os.Stat(filepath.Join(link, "marker")); err != nil {
		t.Fatalf("junction does not resolve to target: %v", err)
	}

	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !isCompatLink(linkInfo) {
		t.Fatalf("expected isCompatLink(junction) = true, mode = %v", linkInfo.Mode())
	}

	realInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if isCompatLink(realInfo) {
		t.Fatal("expected isCompatLink(real directory) = false")
	}
}
