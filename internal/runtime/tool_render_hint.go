package runtime

import (
	"path"
	"regexp"
	"strings"

	"builder/internal/transcript"
	"mvdan.cc/sh/v3/syntax"
)

var sedPrintRangePattern = regexp.MustCompile(`^\d+(?:,\d+)?p$`)

func detectShellRenderHint(command string) *transcript.ToolRenderHint {
	args, ok := parseSimpleShellCommand(command)
	if !ok || len(args) == 0 {
		return nil
	}

	name := normalizeCommandName(args[0])
	switch name {
	case "cat":
		filePath, ok := parseCatFileArg(args)
		if !ok {
			return nil
		}
		return &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource, Path: filePath, ResultOnly: true}
	case "nl":
		filePath, ok := parseNlFileArg(args)
		if !ok {
			return nil
		}
		return &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource, Path: filePath, ResultOnly: true}
	case "sed":
		filePath, ok := parseSedFileArg(args)
		if !ok {
			return nil
		}
		return &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource, Path: filePath, ResultOnly: true}
	default:
		return nil
	}
}

func parseSimpleShellCommand(command string) ([]string, bool) {
	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil || file == nil || len(file.Stmts) != 1 {
		return nil, false
	}

	stmt := file.Stmts[0]
	if stmt == nil || stmt.Cmd == nil || stmt.Negated || stmt.Background || stmt.Coprocess || len(stmt.Redirs) > 0 {
		return nil, false
	}

	callExpr, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(callExpr.Assigns) > 0 || len(callExpr.Args) == 0 {
		return nil, false
	}

	args := make([]string, 0, len(callExpr.Args))
	for _, arg := range callExpr.Args {
		literal, ok := literalWord(arg)
		if !ok || literal == "" {
			return nil, false
		}
		args = append(args, literal)
	}

	return args, true
}

func literalWord(word *syntax.Word) (string, bool) {
	if word == nil || len(word.Parts) == 0 {
		return "", false
	}

	var out strings.Builder
	for _, part := range word.Parts {
		switch x := part.(type) {
		case *syntax.Lit:
			out.WriteString(x.Value)
		case *syntax.SglQuoted:
			out.WriteString(x.Value)
		case *syntax.DblQuoted:
			for _, nested := range x.Parts {
				lit, ok := nested.(*syntax.Lit)
				if !ok {
					return "", false
				}
				out.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}

	return out.String(), true
}

func normalizeCommandName(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	base := path.Base(strings.ReplaceAll(command, "\\", "/"))
	base = strings.ToLower(strings.TrimSpace(base))
	base = strings.TrimSuffix(base, ".exe")
	base = strings.TrimSuffix(base, ".cmd")
	base = strings.TrimSuffix(base, ".bat")
	base = strings.TrimSuffix(base, ".com")
	return base
}

func parseCatFileArg(args []string) (string, bool) {
	if len(args) == 2 && !strings.HasPrefix(args[1], "-") {
		return args[1], true
	}
	if len(args) == 3 && args[1] == "--" && strings.TrimSpace(args[2]) != "" {
		return args[2], true
	}
	return "", false
}

func parseNlFileArg(args []string) (string, bool) {
	if len(args) == 2 && !strings.HasPrefix(args[1], "-") {
		return args[1], true
	}
	if len(args) == 3 && (args[1] == "-ba" || args[1] == "--body-numbering=a") && strings.TrimSpace(args[2]) != "" {
		return args[2], true
	}
	if len(args) == 4 && (args[1] == "-ba" || args[1] == "--body-numbering=a") && args[2] == "--" && strings.TrimSpace(args[3]) != "" {
		return args[3], true
	}
	return "", false
}

func parseSedFileArg(args []string) (string, bool) {
	if len(args) < 4 || args[1] != "-n" || !sedPrintRangePattern.MatchString(args[2]) {
		return "", false
	}

	if len(args) == 4 {
		if strings.HasPrefix(args[3], "-") {
			return "", false
		}
		return args[3], true
	}

	if len(args) == 5 && args[3] == "--" && strings.TrimSpace(args[4]) != "" {
		return args[4], true
	}

	return "", false
}
