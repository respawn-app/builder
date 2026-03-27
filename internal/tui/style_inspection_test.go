package tui

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

type styleLineState struct {
	hasForeground bool
	faint         bool
}

func inspectForegroundColors(text string) []rgbColor {
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

func styleStatesAtLineStarts(text string) []styleLineState {
	parser := xansi.GetParser()
	defer xansi.PutParser(parser)

	states := []styleLineState{{}}
	current := styleLineState{}
	state := byte(0)
	input := text
	for len(input) > 0 {
		seq, width, n, newState := xansi.GraphemeWidth.DecodeSequenceInString(input, state, parser)
		if n <= 0 {
			break
		}
		state = newState
		input = input[n:]
		if width > 0 {
			continue
		}
		if strings.Contains(seq, "\n") {
			for range strings.Count(seq, "\n") {
				states = append(states, current)
			}
			continue
		}
		if xansi.Cmd(parser.Command()).Final() != 'm' {
			continue
		}
		current = applyStyleLineState(current, parser.Params())
	}
	return states
}

func applyStyleLineState(current styleLineState, params xansi.Params) styleLineState {
	if len(params) == 0 {
		return styleLineState{}
	}
	for idx := 0; idx < len(params); {
		param, _, ok := params.Param(idx, 0)
		if !ok {
			break
		}
		switch {
		case param == 0:
			current = styleLineState{}
			idx++
		case param == 2:
			current.faint = true
			idx++
		case param == 22:
			current.faint = false
			idx++
		case param == 39:
			current.hasForeground = false
			idx++
		case (30 <= param && param <= 37) || (90 <= param && param <= 97):
			current.hasForeground = true
			idx++
		case param == 38:
			_, consumed, ok := parseANSIForegroundColor(params, idx)
			if !ok {
				idx++
				continue
			}
			current.hasForeground = true
			idx += consumed
		default:
			idx++
		}
	}
	return current
}

func assertHasForegroundOwnership(t *testing.T, text string, owner rgbColor) {
	t.Helper()
	colors := inspectForegroundColors(text)
	if !containsRGBColor(colors, owner) {
		t.Fatalf("expected foreground ownership color %s in %q", owner.hexString(), text)
	}
}

func assertHasNonOwnerForeground(t *testing.T, text string, owner rgbColor) {
	t.Helper()
	colors := inspectForegroundColors(text)
	if !containsNonOwnerRGBColor(colors, owner) {
		t.Fatalf("expected at least one non-owner foreground color in %q", text)
	}
}

func assertNoBackgroundStyles(t *testing.T, text string) {
	t.Helper()
	if containsBackgroundSGR(text) {
		t.Fatalf("expected no background styles in %q", text)
	}
}

func assertRestoresForegroundAfterReset(t *testing.T, text string, owner rgbColor) {
	t.Helper()
	reset := "\x1b[0;" + strings.Join(foregroundParams(owner), ";") + "m"
	if !strings.Contains(text, reset) && !strings.HasPrefix(text, foregroundEscape(owner)) {
		t.Fatalf("expected reset to restore %s in %q", owner.hexString(), text)
	}
}

func assertSubduedRendering(t *testing.T, text string) {
	t.Helper()
	if !strings.Contains(text, ";2m") {
		t.Fatalf("expected subdued/faint styling in %q", text)
	}
}

func containsRGBColor(colors []rgbColor, target rgbColor) bool {
	for _, color := range colors {
		if color == target {
			return true
		}
	}
	return false
}

func containsNonOwnerRGBColor(colors []rgbColor, owner rgbColor) bool {
	for _, color := range colors {
		if color != owner {
			return true
		}
	}
	return false
}
