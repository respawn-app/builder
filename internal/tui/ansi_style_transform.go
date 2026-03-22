package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
)

const (
	mutedColorChromaFactor   = 0.42
	mutedColorLightnessBlend = 0.28
)

type rgbColor struct {
	r int
	g int
	b int
}

type ansiStyleTransform struct {
	DefaultForeground   *rgbColor
	TransformForeground func(rgbColor) rgbColor
}

type perceptualColorMuter struct {
	targetLightness float64
	chromaFactor    float64
	lightnessBlend  float64
}

type oklabColor struct {
	l float64
	a float64
	b float64
}

type oklchColor struct {
	l float64
	c float64
	h float64
}

func muteANSIOutput(text string, target rgbColor) string {
	if text == "" {
		return text
	}
	muter := newPerceptualColorMuter(target)
	return applyANSIStyleTransform(text, ansiStyleTransform{
		DefaultForeground:   &target,
		TransformForeground: muter.Mute,
	})
}

func newPerceptualColorMuter(target rgbColor) perceptualColorMuter {
	anchor := target.toOKLab()
	return perceptualColorMuter{
		targetLightness: anchor.l,
		chromaFactor:    mutedColorChromaFactor,
		lightnessBlend:  mutedColorLightnessBlend,
	}
}

func (m perceptualColorMuter) Mute(color rgbColor) rgbColor {
	muted := color.toOKLCH()
	muted.c *= m.chromaFactor
	muted.l = mixFloat(muted.l, m.targetLightness, m.lightnessBlend)
	return muted.toRGB()
}

func applyANSIStyleTransform(text string, transform ansiStyleTransform) string {
	if text == "" {
		return text
	}
	if transform.DefaultForeground == nil && transform.TransformForeground == nil {
		return text
	}

	parser := xansi.GetParser()
	defer xansi.PutParser(parser)

	var out strings.Builder
	out.Grow(len(text) + 32)
	if transform.DefaultForeground != nil {
		out.WriteString(foregroundEscape(*transform.DefaultForeground))
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
	if transform.DefaultForeground != nil {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}

func rewriteTransformedSGR(params xansi.Params, transform ansiStyleTransform) string {
	if len(params) == 0 {
		if transform.DefaultForeground == nil {
			return "\x1b[m"
		}
		return "\x1b[0;" + strings.Join(foregroundParams(*transform.DefaultForeground), ";") + "m"
	}

	rewritten := make([]string, 0, len(params)+5)
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
			rewritten = append(rewritten, transformedForegroundParams(ansi16Color(param-30), transform)...)
			needsDefaultForeground = false
			idx++
		case 90 <= param && param <= 97:
			rewritten = append(rewritten, transformedForegroundParams(ansi16Color(param-82), transform)...)
			needsDefaultForeground = false
			idx++
		case param == 38:
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

func (c rgbColor) toOKLab() oklabColor {
	r := srgbToLinear(float64(c.r) / 255)
	g := srgbToLinear(float64(c.g) / 255)
	b := srgbToLinear(float64(c.b) / 255)

	l := 0.4122214708*r + 0.5363325363*g + 0.0514459929*b
	m := 0.2119034982*r + 0.6806995451*g + 0.1073969566*b
	s := 0.0883024619*r + 0.2817188376*g + 0.6299787005*b

	lRoot := math.Cbrt(l)
	mRoot := math.Cbrt(m)
	sRoot := math.Cbrt(s)

	return oklabColor{
		l: 0.2104542553*lRoot + 0.7936177850*mRoot - 0.0040720468*sRoot,
		a: 1.9779984951*lRoot - 2.4285922050*mRoot + 0.4505937099*sRoot,
		b: 0.0259040371*lRoot + 0.7827717662*mRoot - 0.8086757660*sRoot,
	}
}

func (c rgbColor) toOKLCH() oklchColor {
	lab := c.toOKLab()
	return lab.toOKLCH()
}

func (c oklabColor) toOKLCH() oklchColor {
	return oklchColor{
		l: c.l,
		c: math.Hypot(c.a, c.b),
		h: math.Atan2(c.b, c.a),
	}
}

func (c oklchColor) toRGB() rgbColor {
	return oklabColor{
		l: c.l,
		a: c.c * math.Cos(c.h),
		b: c.c * math.Sin(c.h),
	}.toRGB()
}

func (c oklabColor) toRGB() rgbColor {
	lRoot := c.l + 0.3963377774*c.a + 0.2158037573*c.b
	mRoot := c.l - 0.1055613458*c.a - 0.0638541728*c.b
	sRoot := c.l - 0.0894841775*c.a - 1.2914855480*c.b

	l := lRoot * lRoot * lRoot
	m := mRoot * mRoot * mRoot
	s := sRoot * sRoot * sRoot

	r := 4.0767416621*l - 3.3077115913*m + 0.2309699292*s
	g := -1.2684380046*l + 2.6097574011*m - 0.3413193965*s
	b := -0.0041960863*l - 0.7034186147*m + 1.7076147010*s

	return rgbColor{
		r: linearToSRGB8(r),
		g: linearToSRGB8(g),
		b: linearToSRGB8(b),
	}
}

func srgbToLinear(value float64) float64 {
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

func linearToSRGB8(value float64) int {
	value = clampUnit(value)
	if value <= 0.0031308 {
		value *= 12.92
	} else {
		value = 1.055*math.Pow(value, 1.0/2.4) - 0.055
	}
	return clampColor(int(math.Round(value * 255)))
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func mixFloat(from, to, weight float64) float64 {
	return from + (to-from)*weight
}

func clampColor(value int) int {
	return clamp(value, 0, 255)
}
