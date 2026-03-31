package tui

import xansi "github.com/charmbracelet/x/ansi"

func containsBackgroundSGR(text string) bool {
	parser := xansi.GetParser()
	defer xansi.PutParser(parser)

	state := byte(0)
	input := text
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
		if sgrHasBackground(parser.Params()) {
			return true
		}
	}
	return false
}

func sgrHasBackground(params xansi.Params) bool {
	for idx := 0; idx < len(params); {
		param, _, ok := params.Param(idx, -1)
		if !ok || param < 0 {
			idx++
			continue
		}
		switch {
		case param == 38:
			idx += skipExtendedColorParams(params, idx)
		case param == 48:
			return true
		case param == 49:
			return true
		case 40 <= param && param <= 47:
			return true
		case 100 <= param && param <= 107:
			return true
		default:
			idx++
		}
	}
	return false
}

func skipExtendedColorParams(params xansi.Params, start int) int {
	mode, _, ok := params.Param(start+1, -1)
	if !ok || mode < 0 {
		return 1
	}
	if mode == 5 {
		return 3
	}
	if mode != 2 {
		return 1
	}
	r1, _, ok1 := params.Param(start+2, -1)
	g1, _, ok2 := params.Param(start+3, -1)
	b1, _, ok3 := params.Param(start+4, -1)
	if ok1 && ok2 && ok3 && r1 >= 0 && g1 >= 0 && b1 >= 0 {
		return 5
	}
	r2, _, ok1 := params.Param(start+3, -1)
	g2, _, ok2 := params.Param(start+4, -1)
	b2, _, ok3 := params.Param(start+5, -1)
	if ok1 && ok2 && ok3 && r2 >= 0 && g2 >= 0 && b2 >= 0 {
		return 6
	}
	return 1
}
