package theme

import "github.com/charmbracelet/lipgloss"

type Color struct {
	ANSI      string
	ANSI256   string
	TrueColor string
}

func (c Color) Lipgloss() lipgloss.CompleteColor {
	return lipgloss.CompleteColor{ANSI: c.ANSI, ANSI256: c.ANSI256, TrueColor: c.TrueColor}
}

type AdaptiveColor struct {
	Light Color
	Dark  Color
}

func (c AdaptiveColor) Adaptive() lipgloss.CompleteAdaptiveColor {
	return lipgloss.CompleteAdaptiveColor{
		Light: c.Light.Lipgloss(),
		Dark:  c.Dark.Lipgloss(),
	}
}

func (c AdaptiveColor) Resolve(themeName string) Color {
	if Resolve(themeName) == Light {
		return c.Light
	}
	return c.Dark
}

type AppPalette struct {
	Primary    AdaptiveColor
	Secondary  AdaptiveColor
	Foreground AdaptiveColor
	Muted      AdaptiveColor
	Border     AdaptiveColor
	ModeBg     AdaptiveColor
	ModeText   AdaptiveColor
	ChatBg     AdaptiveColor
	InputBg    AdaptiveColor
}

type TranscriptPalette struct {
	Foreground           AdaptiveColor
	Subdued              AdaptiveColor
	SelectionBackground  AdaptiveColor
	SelectionForeground  AdaptiveColor
	User                 AdaptiveColor
	Assistant            AdaptiveColor
	Tool                 AdaptiveColor
	ToolSuccess          AdaptiveColor
	ToolError            AdaptiveColor
	System               AdaptiveColor
	Success              AdaptiveColor
	Warning              AdaptiveColor
	Error                AdaptiveColor
	Compaction           AdaptiveColor
	DiffAddBackground    AdaptiveColor
	DiffRemoveBackground AdaptiveColor
}

type StatusPalette struct {
	Success      AdaptiveColor
	Warning      AdaptiveColor
	Error        AdaptiveColor
	ContextEmpty AdaptiveColor
}

type Palette struct {
	App        AppPalette
	Transcript TranscriptPalette
	Status     StatusPalette
}

type ResolvedAppPalette struct {
	Primary    Color
	Secondary  Color
	Foreground Color
	Muted      Color
	Border     Color
	ModeBg     Color
	ModeText   Color
	ChatBg     Color
	InputBg    Color
}

type ResolvedTranscriptPalette struct {
	Foreground           Color
	Subdued              Color
	SelectionBackground  Color
	SelectionForeground  Color
	User                 Color
	Assistant            Color
	Tool                 Color
	ToolSuccess          Color
	ToolError            Color
	System               Color
	Success              Color
	Warning              Color
	Error                Color
	Compaction           Color
	DiffAddBackground    Color
	DiffRemoveBackground Color
}

type ResolvedStatusPalette struct {
	Success      Color
	Warning      Color
	Error        Color
	ContextEmpty Color
}

type ResolvedPalette struct {
	Mode       string
	App        ResolvedAppPalette
	Transcript ResolvedTranscriptPalette
	Status     ResolvedStatusPalette
}

func DefaultPalette() Palette {
	return defaultPalette
}

func ResolvePalette(themeName string) ResolvedPalette {
	return defaultPalette.Resolve(themeName)
}

func (p Palette) Resolve(themeName string) ResolvedPalette {
	resolvedTheme := Resolve(themeName)
	return ResolvedPalette{
		Mode: resolvedTheme,
		App: ResolvedAppPalette{
			Primary:    p.App.Primary.Resolve(resolvedTheme),
			Secondary:  p.App.Secondary.Resolve(resolvedTheme),
			Foreground: p.App.Foreground.Resolve(resolvedTheme),
			Muted:      p.App.Muted.Resolve(resolvedTheme),
			Border:     p.App.Border.Resolve(resolvedTheme),
			ModeBg:     p.App.ModeBg.Resolve(resolvedTheme),
			ModeText:   p.App.ModeText.Resolve(resolvedTheme),
			ChatBg:     p.App.ChatBg.Resolve(resolvedTheme),
			InputBg:    p.App.InputBg.Resolve(resolvedTheme),
		},
		Transcript: ResolvedTranscriptPalette{
			Foreground:           p.Transcript.Foreground.Resolve(resolvedTheme),
			Subdued:              p.Transcript.Subdued.Resolve(resolvedTheme),
			SelectionBackground:  p.Transcript.SelectionBackground.Resolve(resolvedTheme),
			SelectionForeground:  p.Transcript.SelectionForeground.Resolve(resolvedTheme),
			User:                 p.Transcript.User.Resolve(resolvedTheme),
			Assistant:            p.Transcript.Assistant.Resolve(resolvedTheme),
			Tool:                 p.Transcript.Tool.Resolve(resolvedTheme),
			ToolSuccess:          p.Transcript.ToolSuccess.Resolve(resolvedTheme),
			ToolError:            p.Transcript.ToolError.Resolve(resolvedTheme),
			System:               p.Transcript.System.Resolve(resolvedTheme),
			Success:              p.Transcript.Success.Resolve(resolvedTheme),
			Warning:              p.Transcript.Warning.Resolve(resolvedTheme),
			Error:                p.Transcript.Error.Resolve(resolvedTheme),
			Compaction:           p.Transcript.Compaction.Resolve(resolvedTheme),
			DiffAddBackground:    p.Transcript.DiffAddBackground.Resolve(resolvedTheme),
			DiffRemoveBackground: p.Transcript.DiffRemoveBackground.Resolve(resolvedTheme),
		},
		Status: ResolvedStatusPalette{
			Success:      p.Status.Success.Resolve(resolvedTheme),
			Warning:      p.Status.Warning.Resolve(resolvedTheme),
			Error:        p.Status.Error.Resolve(resolvedTheme),
			ContextEmpty: p.Status.ContextEmpty.Resolve(resolvedTheme),
		},
	}
}

var defaultPalette = Palette{
	App: AppPalette{
		Primary: AdaptiveColor{
			Light: Color{ANSI: "4", ANSI256: "26", TrueColor: "#005CC5"},
			Dark:  Color{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"},
		},
		Secondary: AdaptiveColor{
			Light: Color{ANSI: "2", ANSI256: "29", TrueColor: "#1B7F5A"},
			Dark:  Color{ANSI: "6", ANSI256: "79", TrueColor: "#7FDBA6"},
		},
		Foreground: AdaptiveColor{
			Light: Color{ANSI: "0", ANSI256: "235", TrueColor: "#1F2328"},
			Dark:  Color{ANSI: "7", ANSI256: "252", TrueColor: "#D7DAE0"},
		},
		Muted: AdaptiveColor{
			Light: Color{ANSI: "8", ANSI256: "244", TrueColor: "#6A737D"},
			Dark:  Color{ANSI: "8", ANSI256: "243", TrueColor: "#7D8590"},
		},
		Border: AdaptiveColor{
			Light: Color{ANSI: "7", ANSI256: "250", TrueColor: "#D0D7DE"},
			Dark:  Color{ANSI: "8", ANSI256: "240", TrueColor: "#3D444D"},
		},
		ModeBg: AdaptiveColor{
			Light: Color{ANSI: "7", ANSI256: "254", TrueColor: "#EEF2F6"},
			Dark:  Color{ANSI: "8", ANSI256: "238", TrueColor: "#2D333B"},
		},
		ModeText: AdaptiveColor{
			Light: Color{ANSI: "0", ANSI256: "235", TrueColor: "#1F2328"},
			Dark:  Color{ANSI: "7", ANSI256: "252", TrueColor: "#D7DAE0"},
		},
		ChatBg: AdaptiveColor{
			Light: Color{ANSI: "7", ANSI256: "255", TrueColor: "#F6F8FA"},
			Dark:  Color{ANSI: "0", ANSI256: "235", TrueColor: "#161B22"},
		},
		InputBg: AdaptiveColor{
			Light: Color{ANSI: "7", ANSI256: "254", TrueColor: "#FFFFFF"},
			Dark:  Color{ANSI: "0", ANSI256: "236", TrueColor: "#22272E"},
		},
	},
	Transcript: TranscriptPalette{
		Foreground: AdaptiveColor{
			Light: Color{ANSI: "0", ANSI256: "237", TrueColor: "#383A42"},
			Dark:  Color{ANSI: "7", ANSI256: "145", TrueColor: "#ABB2BF"},
		},
		Subdued: AdaptiveColor{
			Light: Color{ANSI: "8", ANSI256: "242", TrueColor: "#5C6370"},
			Dark:  Color{ANSI: "8", ANSI256: "244", TrueColor: "#7F848E"},
		},
		SelectionBackground: AdaptiveColor{
			Light: Color{ANSI: "7", ANSI256: "153", TrueColor: "#DDEBFF"},
			Dark:  Color{ANSI: "8", ANSI256: "24", TrueColor: "#274A63"},
		},
		SelectionForeground: AdaptiveColor{
			Light: Color{ANSI: "0", ANSI256: "235", TrueColor: "#1F2328"},
			Dark:  Color{ANSI: "7", ANSI256: "252", TrueColor: "#D7DAE0"},
		},
		User: AdaptiveColor{
			Light: Color{ANSI: "4", ANSI256: "26", TrueColor: "#005CC5"},
			Dark:  Color{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"},
		},
		Assistant: AdaptiveColor{
			Light: Color{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
			Dark:  Color{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
		},
		Tool: AdaptiveColor{
			Light: Color{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"},
			Dark:  Color{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"},
		},
		ToolSuccess: AdaptiveColor{
			Light: Color{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
			Dark:  Color{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
		},
		ToolError: AdaptiveColor{
			Light: Color{ANSI: "1", ANSI256: "160", TrueColor: "#D73A49"},
			Dark:  Color{ANSI: "1", ANSI256: "204", TrueColor: "#E06C75"},
		},
		System: AdaptiveColor{
			Light: Color{ANSI: "8", ANSI256: "244", TrueColor: "#6A737D"},
			Dark:  Color{ANSI: "7", ANSI256: "145", TrueColor: "#ABB2BF"},
		},
		Success: AdaptiveColor{
			Light: Color{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
			Dark:  Color{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
		},
		Warning: AdaptiveColor{
			Light: Color{ANSI: "3", ANSI256: "136", TrueColor: "#8A5A00"},
			Dark:  Color{ANSI: "3", ANSI256: "180", TrueColor: "#E5C07B"},
		},
		Error: AdaptiveColor{
			Light: Color{ANSI: "1", ANSI256: "160", TrueColor: "#D73A49"},
			Dark:  Color{ANSI: "1", ANSI256: "204", TrueColor: "#E06C75"},
		},
		Compaction: AdaptiveColor{
			Light: Color{ANSI: "3", ANSI256: "136", TrueColor: "#8A5A00"},
			Dark:  Color{ANSI: "3", ANSI256: "180", TrueColor: "#E5C07B"},
		},
		DiffAddBackground: AdaptiveColor{
			Light: Color{ANSI: "2", ANSI256: "194", TrueColor: "#E6FFED"},
			Dark:  Color{ANSI: "2", ANSI256: "22", TrueColor: "#1F2A22"},
		},
		DiffRemoveBackground: AdaptiveColor{
			Light: Color{ANSI: "1", ANSI256: "224", TrueColor: "#FFECEF"},
			Dark:  Color{ANSI: "1", ANSI256: "52", TrueColor: "#2B1F22"},
		},
	},
	Status: StatusPalette{
		Success: AdaptiveColor{
			Light: Color{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
			Dark:  Color{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
		},
		Warning: AdaptiveColor{
			Light: Color{ANSI: "3", ANSI256: "136", TrueColor: "#9A6700"},
			Dark:  Color{ANSI: "3", ANSI256: "180", TrueColor: "#E5C07B"},
		},
		Error: AdaptiveColor{
			Light: Color{ANSI: "1", ANSI256: "160", TrueColor: "#CB2431"},
			Dark:  Color{ANSI: "1", ANSI256: "203", TrueColor: "#F97583"},
		},
		ContextEmpty: AdaptiveColor{
			Light: Color{ANSI: "8", ANSI256: "247", TrueColor: "#A0A1A7"},
			Dark:  Color{ANSI: "8", ANSI256: "59", TrueColor: "#5C6370"},
		},
	},
}
