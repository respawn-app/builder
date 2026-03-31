package tui

import (
	"github.com/alecthomas/chroma/v2"
	glamouransi "github.com/charmbracelet/glamour/ansi"
)

func withTransparentChromaBackgrounds(base *chroma.Style, appForeground chroma.Colour) *chroma.Style {
	if base == nil {
		return nil
	}
	builder := base.Builder()
	baseText := base.Get(chroma.Text).Colour
	builder.Transform(func(entry chroma.StyleEntry) chroma.StyleEntry {
		entry.Background = 0
		if entry.Colour == baseText {
			entry.Colour = appForeground
		}
		return entry
	})
	overridden, err := builder.Build()
	if err != nil {
		return base
	}
	return overridden
}

func markdownStylePrimitives(cfg *glamouransi.StyleConfig) []*glamouransi.StylePrimitive {
	if cfg == nil {
		return nil
	}
	primitives := []*glamouransi.StylePrimitive{
		&cfg.Document.StylePrimitive,
		&cfg.BlockQuote.StylePrimitive,
		&cfg.Paragraph.StylePrimitive,
		&cfg.List.StyleBlock.StylePrimitive,
		&cfg.Heading.StylePrimitive,
		&cfg.H1.StylePrimitive,
		&cfg.H2.StylePrimitive,
		&cfg.H3.StylePrimitive,
		&cfg.H4.StylePrimitive,
		&cfg.H5.StylePrimitive,
		&cfg.H6.StylePrimitive,
		&cfg.Text,
		&cfg.Strikethrough,
		&cfg.Emph,
		&cfg.Strong,
		&cfg.HorizontalRule,
		&cfg.Item,
		&cfg.Enumeration,
		&cfg.Task.StylePrimitive,
		&cfg.Link,
		&cfg.LinkText,
		&cfg.Image,
		&cfg.ImageText,
		&cfg.Code.StylePrimitive,
		&cfg.CodeBlock.StyleBlock.StylePrimitive,
		&cfg.Table.StyleBlock.StylePrimitive,
		&cfg.DefinitionList.StylePrimitive,
		&cfg.DefinitionTerm,
		&cfg.DefinitionDescription,
		&cfg.HTMLBlock.StylePrimitive,
		&cfg.HTMLSpan.StylePrimitive,
	}
	if cfg.CodeBlock.Chroma != nil {
		primitives = append(primitives, markdownChromaStylePrimitives(cfg.CodeBlock.Chroma)...)
	}
	return primitives
}

func markdownChromaStylePrimitives(chromaCfg *glamouransi.Chroma) []*glamouransi.StylePrimitive {
	if chromaCfg == nil {
		return nil
	}
	return []*glamouransi.StylePrimitive{
		&chromaCfg.Text,
		&chromaCfg.Error,
		&chromaCfg.Comment,
		&chromaCfg.CommentPreproc,
		&chromaCfg.Keyword,
		&chromaCfg.KeywordReserved,
		&chromaCfg.KeywordNamespace,
		&chromaCfg.KeywordType,
		&chromaCfg.Operator,
		&chromaCfg.Punctuation,
		&chromaCfg.Name,
		&chromaCfg.NameBuiltin,
		&chromaCfg.NameTag,
		&chromaCfg.NameAttribute,
		&chromaCfg.NameClass,
		&chromaCfg.NameConstant,
		&chromaCfg.NameDecorator,
		&chromaCfg.NameException,
		&chromaCfg.NameFunction,
		&chromaCfg.NameOther,
		&chromaCfg.Literal,
		&chromaCfg.LiteralNumber,
		&chromaCfg.LiteralDate,
		&chromaCfg.LiteralString,
		&chromaCfg.LiteralStringEscape,
		&chromaCfg.GenericDeleted,
		&chromaCfg.GenericEmph,
		&chromaCfg.GenericInserted,
		&chromaCfg.GenericStrong,
		&chromaCfg.GenericSubheading,
		&chromaCfg.Background,
	}
}

func clearMarkdownBackgrounds(cfg *glamouransi.StyleConfig) {
	for _, primitive := range markdownStylePrimitives(cfg) {
		primitive.BackgroundColor = nil
	}
}
