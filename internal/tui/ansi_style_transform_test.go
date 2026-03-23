package tui

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

func TestMuteANSIOutputPrefixesPreviewForegroundAndFaint(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	base := m.palette().previewColor
	muted := muteANSIOutput("echo hello", base)

	prefix := "\x1b[" + strings.Join(styleParams(ansiStyleTransform{
		DefaultForeground: &base,
		ForceFaint:        true,
	}, false), ";") + "m"
	if !strings.HasPrefix(muted, prefix) {
		t.Fatalf("expected muted output to start with preview+faint style, got %q", muted)
	}
	if !strings.HasSuffix(muted, "\x1b[0m") {
		t.Fatalf("expected muted output to terminate style state, got %q", muted)
	}
}

func TestMuteANSIOutputReappliesPreviewForegroundAndFaintAfterReset(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	base := m.palette().previewColor
	muted := muteANSIOutput("echo \x1b[38;5;81mfoo\x1b[0m bar", base)

	reset := "\x1b[" + strings.Join(styleParams(ansiStyleTransform{
		DefaultForeground: &base,
		ForceFaint:        true,
	}, true), ";") + "m"
	if !strings.Contains(muted, reset+" bar") {
		t.Fatalf("expected reset to restore preview+faint style, got %q", muted)
	}
	if got := xansi.Strip(muted); got != "echo foo bar" {
		t.Fatalf("expected text preserved after muting, got %q", got)
	}
}

func TestMuteANSIOutputReappliesFaintAfterNormalIntensity(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	muted := muteANSIOutput("echo \x1b[22mplain", m.palette().previewColor)
	if !strings.Contains(muted, "\x1b[22;2mplain") {
		t.Fatalf("expected normal-intensity reset to reapply faint, got %q", muted)
	}
	if got := xansi.Strip(muted); got != "echo plain" {
		t.Fatalf("expected text preserved after intensity rewrite, got %q", got)
	}
}

func TestMuteANSIOutputSupportsColonTrueColorSGR(t *testing.T) {
	m := NewModel(WithTheme("light"))
	base := m.palette().previewColor
	muted := muteANSIOutput("\x1b[38:2:255:0:255mhello\x1b[39m world", base)

	if strings.Contains(muted, "\x1b[38:2:255:0:255m") {
		t.Fatalf("expected colon-form truecolor sequence to be normalized during rewrite, got %q", muted)
	}
	if !strings.Contains(muted, "\x1b[38;2;255;0;255;2mhello") {
		t.Fatalf("expected color sequence to keep foreground and add faint, got %q", muted)
	}
	resetToPreview := "\x1b[" + strings.Join(styleParams(ansiStyleTransform{
		DefaultForeground: &base,
		ForceFaint:        true,
	}, false), ";") + "m world"
	if !strings.Contains(muted, resetToPreview) {
		t.Fatalf("expected 39 reset to restore preview+faint style, got %q", muted)
	}
	if got := xansi.Strip(muted); got != "hello world" {
		t.Fatalf("expected colon-form truecolor text preserved, got %q", got)
	}
}

func TestApplyDefaultForegroundPreservesExistingSyntaxColorSGRVerbatim(t *testing.T) {
	base := themeForegroundColor("dark")
	input := "\x1b[91mbright\x1b[39m \x1b[38;5;81midx\x1b[0m \x1b[38;2;1;2;3mtrue\x1b[39m tail"
	out := applyDefaultForeground(input, base)

	if !strings.HasPrefix(out, foregroundEscape(base)) {
		t.Fatalf("expected output to start with default foreground, got %q", out)
	}
	for _, seq := range []string{"\x1b[91m", "\x1b[38;5;81m", "\x1b[38;2;1;2;3m"} {
		if !strings.Contains(out, seq) {
			t.Fatalf("expected existing syntax color sequence %q to be preserved verbatim, got %q", seq, out)
		}
	}
	if !strings.Contains(out, "\x1b["+strings.Join(styleParams(ansiStyleTransform{DefaultForeground: &base}, false), ";")+"m tail") {
		t.Fatalf("expected 39 reset to restore app foreground, got %q", out)
	}
	if !strings.Contains(out, "\x1b[0;"+strings.Join(foregroundParams(base), ";")+"m ") {
		t.Fatalf("expected 0 reset to restore app foreground, got %q", out)
	}
	if got := xansi.Strip(out); got != "bright idx true tail" {
		t.Fatalf("expected text preserved after default foreground rewrite, got %q", got)
	}
}

func extractForegroundTrueColors(text string) []rgbColor {
	parser := xansi.GetParser()
	defer xansi.PutParser(parser)

	state := byte(0)
	input := text
	colors := make([]rgbColor, 0, 8)
	for len(input) > 0 {
		_, width, n, newState := xansi.GraphemeWidth.DecodeSequenceInString(input, state, parser)
		if n <= 0 {
			break
		}
		state = newState
		input = input[n:]
		if width > 0 || xansi.Cmd(parser.Command()).Final() != 'm' {
			continue
		}
		params := parser.Params()
		for idx := 0; idx < len(params); {
			param, _, ok := params.Param(idx, 0)
			if !ok {
				break
			}
			if param == 38 {
				color, consumed, ok := parseANSIForegroundColor(params, idx)
				if ok {
					colors = append(colors, color)
					idx += consumed
					continue
				}
			}
			idx++
		}
	}
	return colors
}

func containsColor(colors []rgbColor, target rgbColor) bool {
	for _, color := range colors {
		if color == target {
			return true
		}
	}
	return false
}

func containsNonPreviewColor(colors []rgbColor, preview rgbColor) bool {
	for _, color := range colors {
		if color != preview {
			return true
		}
	}
	return false
}
