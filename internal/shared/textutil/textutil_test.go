package textutil

import "testing"

func TestNormalizeCRLF(t *testing.T) {
	if got := NormalizeCRLF("a\r\nb\r\n"); got != "a\nb\n" {
		t.Fatalf("unexpected normalization: %q", got)
	}
	if got := NormalizeCRLF("a\rb"); got != "a\rb" {
		t.Fatalf("unexpected single-CR normalization: %q", got)
	}
}

func TestSplitLinesCRLF(t *testing.T) {
	got := SplitLinesCRLF("a\r\nb\n")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "" {
		t.Fatalf("unexpected split result: %#v", got)
	}
}
