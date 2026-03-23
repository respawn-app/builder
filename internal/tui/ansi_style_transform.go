package tui

import (
	"fmt"
	"strconv"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
)

type rgbColor struct {
	r int
	g int
	b int
}

type ansiStyleTransform struct {
	DefaultForeground   *rgbColor
	TransformForeground func(rgbColor) rgbColor
	ForceFaint          bool
}

func muteANSIOutput(text string, target rgbColor) string {
	if text == "" {
		return text
	}
	return applyANSIStyleTransform(text, ansiStyleTransform{
		DefaultForeground: &target,
		ForceFaint:        true,
	})
}

func applyDefaultForeground(text string, target rgbColor) string {
	if text == "" {
		return text
	}
	return applyANSIStyleTransform(text, ansiStyleTransform{
		DefaultForeground: &target,
	})
}

func ApplyThemeDefaultForeground(text, theme string) string {
	return applyDefaultForeground(text, themeForegroundColor(theme))
}

func applyANSIStyleTransform(text string, transform ansiStyleTransform) string {
	if text == "" {
		return text
	}
	if transform.DefaultForeground == nil && transform.TransformForeground == nil && !transform.ForceFaint {
		return text
	}

	parser := xansi.GetParser()
	defer xansi.PutParser(parser)

	var out strings.Builder
	out.Grow(len(text) + 32)
	if prefix := styleEscape(transform, false); prefix != "" {
		out.WriteString(prefix)
	}

	state := byte(0)
	input := text
	for len(input) > 0 {
		seq, width, n, newState := xansi.GraphemeWidth.DecodeSequenceInString(input, state, parser)
		if n <= 0 {
			out.WriteString(input)
			break
		}
		state = newState
		input = input[n:]
		if width > 0 {
			out.WriteString(seq)
			continue
		}
		if xansi.Cmd(parser.Command()).Final() != 'm' {
			out.WriteString(seq)
			continue
		}
		out.WriteString(rewriteTransformedSGR(parser.Params(), transform))
	}
	if styleEscape(transform, true) != "" {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}

func rewriteTransformedSGR(params xansi.Params, transform ansiStyleTransform) string {
	if len(params) == 0 {
		return styleEscape(transform, true)
	}

	rewritten := make([]string, 0, len(params)+6)
	needsDefaultForeground := false

	for idx := 0; idx < len(params); {
		param, _, ok := params.Param(idx, 0)
		if !ok {
			break
		}
		switch {
		case param == 0:
			rewritten = append(rewritten, "0")
			needsDefaultForeground = transform.DefaultForeground != nil
			idx++
		case param == 39:
			if transform.DefaultForeground == nil {
				rewritten = append(rewritten, "39")
			} else {
				needsDefaultForeground = true
			}
			idx++
		case 30 <= param && param <= 37:
			if transform.TransformForeground == nil {
				rewritten = append(rewritten, strconv.Itoa(param))
			} else {
				rewritten = append(rewritten, transformedForegroundParams(ansi16Color(param-30), transform)...)
			}
			needsDefaultForeground = false
			idx++
		case 90 <= param && param <= 97:
			if transform.TransformForeground == nil {
				rewritten = append(rewritten, strconv.Itoa(param))
			} else {
				rewritten = append(rewritten, transformedForegroundParams(ansi16Color(param-82), transform)...)
			}
			needsDefaultForeground = false
			idx++
		case param == 38:
			if transform.TransformForeground == nil {
				copied, consumed, ok := copyANSIForegroundParams(params, idx)
				if !ok {
					rewritten = append(rewritten, strconv.Itoa(param))
					idx++
					continue
				}
				rewritten = append(rewritten, copied...)
				needsDefaultForeground = false
				idx += consumed
				continue
			}
			color, consumed, ok := parseANSIForegroundColor(params, idx)
			if !ok {
				rewritten = append(rewritten, strconv.Itoa(param))
				idx++
				continue
			}
			rewritten = append(rewritten, transformedForegroundParams(color, transform)...)
			needsDefaultForeground = false
			idx += consumed
		default:
			rewritten = append(rewritten, strconv.Itoa(param))
			idx++
		}
	}

	if needsDefaultForeground {
		rewritten = append(rewritten, foregroundParams(*transform.DefaultForeground)...)
	}
	if transform.ForceFaint {
		rewritten = append(rewritten, "2")
	}
	if len(rewritten) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(rewritten, ";") + "m"
}

func transformedForegroundParams(color rgbColor, transform ansiStyleTransform) []string {
	if transform.TransformForeground != nil {
		color = transform.TransformForeground(color)
	}
	return foregroundParams(color)
}

func copyANSIForegroundParams(params xansi.Params, start int) ([]string, int, bool) {
	mode, _, ok := params.Param(start+1, -1)
	if !ok || mode < 0 {
		return nil, 0, false
	}
	if mode == 5 {
		index, _, ok := params.Param(start+2, -1)
		if !ok || index < 0 {
			return nil, 0, false
		}
		return []string{"38", "5", strconv.Itoa(index)}, 3, true
	}
	if mode != 2 {
		return nil, 0, false
	}
	color, consumed, ok := parseTrueColor(params, start+2)
	if !ok {
		return nil, 0, false
	}
	return foregroundParams(color), consumed + 2, true
}

func styleEscape(transform ansiStyleTransform, includeReset bool) string {
	params := styleParams(transform, includeReset)
	if len(params) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(params, ";") + "m"
}

func styleParams(transform ansiStyleTransform, includeReset bool) []string {
	params := make([]string, 0, 6)
	if includeReset {
		params = append(params, "0")
	}
	if transform.DefaultForeground != nil {
		params = append(params, foregroundParams(*transform.DefaultForeground)...)
	}
	if transform.ForceFaint {
		params = append(params, "2")
	}
	return params
}

func parseANSIForegroundColor(params xansi.Params, start int) (rgbColor, int, bool) {
	mode, _, ok := params.Param(start+1, -1)
	if !ok || mode < 0 {
		return rgbColor{}, 0, false
	}
	if mode == 5 {
		index, _, ok := params.Param(start+2, -1)
		if !ok || index < 0 {
			return rgbColor{}, 0, false
		}
		return ansi256Color(index), 3, true
	}
	if mode != 2 {
		return rgbColor{}, 0, false
	}
	if color, consumed, ok := parseTrueColor(params, start+2); ok {
		return color, consumed + 2, true
	}
	return rgbColor{}, 0, false
}

func parseTrueColor(params xansi.Params, start int) (rgbColor, int, bool) {
	r, _, okR := params.Param(start, -1)
	g, _, okG := params.Param(start+1, -1)
	b, _, okB := params.Param(start+2, -1)
	if okR && okG && okB && r >= 0 && g >= 0 && b >= 0 {
		return rgbColor{r: clampColor(r), g: clampColor(g), b: clampColor(b)}, 3, true
	}
	r, _, okR = params.Param(start+1, -1)
	g, _, okG = params.Param(start+2, -1)
	b, _, okB = params.Param(start+3, -1)
	if okR && okG && okB && r >= 0 && g >= 0 && b >= 0 {
		return rgbColor{r: clampColor(r), g: clampColor(g), b: clampColor(b)}, 4, true
	}
	return rgbColor{}, 0, false
}

func foregroundParams(color rgbColor) []string {
	return []string{"38", "2", strconv.Itoa(color.r), strconv.Itoa(color.g), strconv.Itoa(color.b)}
}

func foregroundEscape(color rgbColor) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", color.r, color.g, color.b)
}

func ansi256Color(index int) rgbColor {
	index = clamp(index, 0, 255)
	if index < 16 {
		return ansi16Color(index)
	}
	if index < 232 {
		cube := []int{0, 95, 135, 175, 215, 255}
		value := index - 16
		return rgbColor{
			r: cube[(value/36)%6],
			g: cube[(value/6)%6],
			b: cube[value%6],
		}
	}
	gray := 8 + (index-232)*10
	gray = clampColor(gray)
	return rgbColor{r: gray, g: gray, b: gray}
}

func ansi16Color(index int) rgbColor {
	palette := [...]rgbColor{
		{r: 0, g: 0, b: 0},
		{r: 205, g: 0, b: 0},
		{r: 0, g: 205, b: 0},
		{r: 205, g: 205, b: 0},
		{r: 0, g: 0, b: 238},
		{r: 205, g: 0, b: 205},
		{r: 0, g: 205, b: 205},
		{r: 229, g: 229, b: 229},
		{r: 127, g: 127, b: 127},
		{r: 255, g: 0, b: 0},
		{r: 0, g: 255, b: 0},
		{r: 255, g: 255, b: 0},
		{r: 92, g: 92, b: 255},
		{r: 255, g: 0, b: 255},
		{r: 0, g: 255, b: 255},
		{r: 255, g: 255, b: 255},
	}
	return palette[clamp(index, 0, len(palette)-1)]
}

func clampColor(value int) int {
	return clamp(value, 0, 255)
}
