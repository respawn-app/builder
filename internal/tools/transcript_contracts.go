package tools

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	patchformat "builder/internal/tools/patch/format"
	"builder/internal/transcript"
	"mvdan.cc/sh/v3/syntax"
)

var sedPrintRangePattern = regexp.MustCompile(`^\d+(?:,\d+)?p$`)

func localContract(localBuilder LocalRuntimeBuilder, request RequestExposure, presentation transcript.ToolPresentationKind, renderBehavior transcript.ToolCallRenderBehavior, omitSuccessfulResult bool, buildCallMeta func(ToolCallContext, json.RawMessage) transcript.ToolCallMeta, formatResult func(Result) string) Contract {
	return Contract{
		Runtime: RuntimeContract{Availability: RuntimeAvailabilityLocal, LocalBuilder: localBuilder},
		Request: request,
		Transcript: TranscriptContract{
			Presentation:         presentation,
			RenderBehavior:       renderBehavior,
			OmitSuccessfulResult: omitSuccessfulResult,
			BuildCallMeta:        buildCallMeta,
			FormatResult:         formatResult,
		},
	}
}

func hostedContract(request RequestExposure, presentation transcript.ToolPresentationKind, renderBehavior transcript.ToolCallRenderBehavior, omitSuccessfulResult bool, nativeWebSearch bool, buildCallMeta func(ToolCallContext, json.RawMessage) transcript.ToolCallMeta, formatResult func(Result) string, decodeHostedOutput func(HostedToolOutput) (HostedExecution, bool)) Contract {
	return Contract{
		Runtime: RuntimeContract{
			Availability:       RuntimeAvailabilityHosted,
			NativeWebSearch:    nativeWebSearch,
			DecodeHostedOutput: decodeHostedOutput,
		},
		Request: request,
		Transcript: TranscriptContract{
			Presentation:         presentation,
			RenderBehavior:       renderBehavior,
			OmitSuccessfulResult: omitSuccessfulResult,
			BuildCallMeta:        buildCallMeta,
			FormatResult:         formatResult,
		},
	}
}

func defaultToolCallMeta(toolID ID) func(ToolCallContext, json.RawMessage) transcript.ToolCallMeta {
	return func(ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta {
		command, inlineMeta := formatToolInput(toolID, raw, ctx.DefaultShellTimeoutSeconds)
		command = strings.TrimSpace(command)
		if command == "" {
			command = defaultToolCallFallback
		}
		return transcript.ToolCallMeta{
			ToolName:    string(toolID),
			Command:     command,
			CompactText: command,
			InlineMeta:  inlineMeta,
		}
	}
}

func shellToolCallMeta(toolID ID) func(ToolCallContext, json.RawMessage) transcript.ToolCallMeta {
	return func(ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta {
		command, inlineMeta := formatToolInput(toolID, raw, ctx.DefaultShellTimeoutSeconds)
		command = strings.TrimSpace(command)
		if command == "" {
			command = defaultToolCallFallback
		}
		return transcript.ToolCallMeta{
			ToolName:      string(toolID),
			IsShell:       true,
			UserInitiated: parseShellToolCallUserInitiated(raw),
			Command:       command,
			CompactText:   command,
			InlineMeta:    inlineMeta,
			TimeoutLabel:  inlineMeta,
			RenderHint:    detectShellRenderHint(command),
		}
	}
}

func askQuestionToolCallMeta(toolID ID) func(ToolCallContext, json.RawMessage) transcript.ToolCallMeta {
	return func(ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta {
		question, suggestions, ok := parseAskQuestionToolCall(raw)
		if !ok {
			return defaultToolCallMeta(toolID)(ctx, raw)
		}
		return transcript.ToolCallMeta{
			ToolName:    string(toolID),
			Command:     question,
			CompactText: question,
			Question:    question,
			Suggestions: suggestions,
		}
	}
}

func patchToolCallMeta(toolID ID) func(ToolCallContext, json.RawMessage) transcript.ToolCallMeta {
	return func(ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta {
		detail, compact, rendered, ok := parsePatchToolCall(raw, ctx.WorkingDir)
		if !ok {
			meta := defaultToolCallMeta(toolID)(ctx, raw)
			meta.PatchSummary = meta.CompactText
			meta.PatchDetail = meta.Command
			meta.RenderHint = &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff}
			return meta
		}
		return transcript.ToolCallMeta{
			ToolName:     string(toolID),
			Command:      detail,
			CompactText:  compact,
			PatchSummary: compact,
			PatchDetail:  detail,
			PatchRender:  rendered,
			RenderHint:   &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff},
		}
	}
}

func formatGenericToolResult(result Result) string {
	output := strings.TrimSpace(formatOutputDefault(result.Output))
	if output == "" {
		if result.IsError {
			return "tool failed"
		}
		return "done"
	}
	return output
}

func formatPatchToolResult(result Result) string {
	if !result.IsError {
		return ""
	}
	return formatGenericToolResult(result)
}

func formatViewImageToolResult(result Result) string {
	if summary, ok := formatViewImageOutput(result.Output); ok {
		return summary
	}
	return formatGenericToolResult(result)
}

func formatWebSearchToolResult(result Result) string {
	formatted := strings.TrimSpace(formatRawJSON(result.Output))
	if formatted != "" {
		return formatted
	}
	return formatGenericToolResult(result)
}

func parseShellToolCallUserInitiated(raw json.RawMessage) bool {
	var in struct {
		UserInitiated bool `json:"user_initiated"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return false
	}
	return in.UserInitiated
}

func parseAskQuestionToolCall(raw json.RawMessage) (string, []string, bool) {
	var in struct {
		Question    string   `json:"question"`
		Suggestions []string `json:"suggestions,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", nil, false
	}
	question := strings.TrimSpace(in.Question)
	if question == "" {
		return "", nil, false
	}
	suggestions := make([]string, 0, len(in.Suggestions))
	for _, suggestion := range in.Suggestions {
		trimmed := strings.TrimSpace(suggestion)
		if trimmed == "" {
			continue
		}
		suggestions = append(suggestions, trimmed)
	}
	return question, suggestions, true
}

func parsePatchToolCall(raw json.RawMessage, cwd string) (detail string, compact string, rendered *patchformat.RenderedPatch, ok bool) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", "", nil, false
	}
	patchRaw, ok := input["patch"]
	if !ok {
		return "", "", nil, false
	}
	var patchText string
	if err := json.Unmarshal(patchRaw, &patchText); err != nil {
		return "", "", nil, false
	}
	trimmedPatch := strings.TrimSpace(patchText)
	if trimmedPatch == "" {
		return "", "", nil, false
	}
	r := patchformat.Render(patchText, cwd)
	return r.DetailText(), r.SummaryText(), &r, true
}

func detectShellRenderHint(command string) *transcript.ToolRenderHint {
	defaultHint := &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell}
	args, ok := parseSimpleShellCommand(command)
	if !ok || len(args) == 0 {
		return defaultHint
	}

	name := normalizeCommandName(args[0])
	switch name {
	case "cat":
		filePath, ok := parseCatFileArg(args)
		if !ok {
			return defaultHint
		}
		return &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource, Path: filePath, ResultOnly: true}
	case "nl":
		filePath, ok := parseNlFileArg(args)
		if !ok {
			return defaultHint
		}
		return &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource, Path: filePath, ResultOnly: true}
	case "sed":
		filePath, ok := parseSedFileArg(args)
		if !ok {
			return defaultHint
		}
		return &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource, Path: filePath, ResultOnly: true}
	default:
		return defaultHint
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

func decodeHostedWebSearchOutput(item HostedToolOutput) (HostedExecution, bool) {
	raw := item.Raw
	if len(raw) == 0 || !json.Valid(raw) {
		return HostedExecution{}, false
	}
	var payload struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Status string `json:"status"`
		Action struct {
			Type    string `json:"type"`
			Query   string `json:"query"`
			URL     string `json:"url"`
			Pattern string `json:"pattern"`
		} `json:"action"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return HostedExecution{}, false
	}
	if strings.TrimSpace(payload.Type) != "web_search_call" {
		return HostedExecution{}, false
	}
	callID := strings.TrimSpace(payload.ID)
	if callID == "" {
		callID = strings.TrimSpace(item.ID)
	}
	if callID == "" {
		callID = strings.TrimSpace(item.CallID)
	}
	if callID == "" {
		return HostedExecution{}, false
	}
	input := map[string]string{}
	actionType := strings.TrimSpace(payload.Action.Type)
	if actionType != "" {
		input["action"] = actionType
	}
	query := strings.TrimSpace(payload.Action.Query)
	if url := strings.TrimSpace(payload.Action.URL); url != "" {
		if query == "" {
			query = url
		}
		input["url"] = url
	}
	if pattern := strings.TrimSpace(payload.Action.Pattern); pattern != "" {
		if query == "" {
			query = pattern
		}
		input["pattern"] = pattern
	}
	if query == "" {
		if actionType != "" {
			query = actionType
		} else {
			query = "web search"
		}
	}
	input["query"] = query
	inputRaw, err := json.Marshal(input)
	if err != nil {
		return HostedExecution{}, false
	}
	output := append(json.RawMessage(nil), raw...)
	if !json.Valid(output) {
		output = mustJSON(map[string]any{"raw": string(raw)})
	}
	isError := strings.EqualFold(strings.TrimSpace(payload.Status), "failed")
	return HostedExecution{
		Call: HostedCall{
			ID:    callID,
			Name:  ToolWebSearch,
			Input: inputRaw,
		},
		Result: Result{
			CallID:  callID,
			Name:    ToolWebSearch,
			Output:  output,
			IsError: isError,
		},
	}, true
}

func formatToolInput(toolID ID, raw json.RawMessage, shellTimeoutSeconds int) (string, string) {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return strings.TrimSpace(string(raw)), ""
	}
	obj, ok := payload.(map[string]any)
	if !ok {
		return renderPlain(payload), ""
	}
	if toolID == ToolWriteStdin {
		sessionID, _ := asInt(obj["session_id"])
		chars, _ := asString(obj["chars"])
		if strings.TrimSpace(chars) == "" {
			if yieldTimeMS, ok := asInt(obj["yield_time_ms"]); ok && yieldTimeMS > 0 {
				return fmt.Sprintf("Polled session %d for %s", sessionID, formatWriteStdinPollDuration(time.Duration(yieldTimeMS)*time.Millisecond)), ""
			}
			return fmt.Sprintf("poll session %d", sessionID), ""
		}
		return fmt.Sprintf("write stdin session %d", sessionID), ""
	}
	if cmd, ok := asString(obj["cmd"]); ok && toolID == ToolExecCommand {
		return cmd, ""
	}
	if cmd, ok := asString(obj["command"]); ok {
		inlineMeta := ""
		if secs, ok := asInt(obj["timeout_seconds"]); ok && secs > 0 {
			inlineMeta = "timeout: " + formatDurationShort(time.Duration(secs)*time.Second)
		} else if toolID == ToolShell {
			if shellTimeoutSeconds <= 0 {
				shellTimeoutSeconds = DefaultShellTimeoutSeconds
			}
			inlineMeta = "timeout: " + formatDurationShort(time.Duration(shellTimeoutSeconds)*time.Second)
		}
		return cmd, inlineMeta
	}
	if toolID == ToolWebSearch {
		if query, ok := asString(obj["query"]); ok {
			return query, ""
		}
	}
	if toolID == ToolAskQuestion {
		if question, ok := asString(obj["question"]); ok {
			return question, ""
		}
	}
	return renderPlain(payload), ""
}

func formatOutputDefault(raw json.RawMessage) string {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return strings.TrimSpace(string(raw))
	}
	obj, ok := payload.(map[string]any)
	if !ok {
		return renderPlain(payload)
	}

	if msg, ok := asString(obj["error"]); ok {
		return msg
	}
	if out, ok := asString(obj["output"]); ok {
		var notes []string
		if code, ok := asInt(obj["exit_code"]); ok && code != 0 {
			notes = append(notes, fmt.Sprintf("exit code %d", code))
		}
		if len(notes) == 0 {
			return out
		}
		if strings.TrimSpace(out) == "" {
			return strings.Join(notes, ", ")
		}
		return out + "\n" + strings.Join(notes, ", ")
	}
	if answer, ok := asString(obj["answer"]); ok {
		return answer
	}
	return renderPlain(payload)
}

func formatViewImageOutput(raw json.RawMessage) (string, bool) {
	var items []struct {
		Type     string `json:"type"`
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return "", false
	}
	if len(items) == 0 {
		return "", false
	}

	labels := make([]string, 0, len(items))
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "input_image":
			labels = append(labels, "attached image")
		case "input_file":
			filename := strings.TrimSpace(item.Filename)
			if filename == "" {
				labels = append(labels, "attached PDF")
				continue
			}
			labels = append(labels, "attached PDF: "+filename)
		default:
			labels = append(labels, "attached multimodal content")
		}
	}
	if len(labels) == 0 {
		return "", false
	}
	return strings.Join(labels, "\n"), true
}

func formatRawJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if !json.Valid(raw) {
		return strings.TrimSpace(string(raw))
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return strings.TrimSpace(string(raw))
	}
	formatted, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(formatted)
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func formatDurationShort(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	total := int(d.Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60

	parts := make([]string, 0, 3)
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, "")
}

func formatWriteStdinPollDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return d.String()
}

func renderPlain(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		if len(x) == 0 {
			return "[]"
		}
		lines := make([]string, 0, len(x))
		for _, item := range x {
			rendered := strings.TrimSpace(renderPlain(item))
			if rendered == "" {
				continue
			}
			itemLines := strings.Split(rendered, "\n")
			lines = append(lines, "- "+itemLines[0])
			for _, line := range itemLines[1:] {
				lines = append(lines, "  "+line)
			}
		}
		return strings.Join(lines, "\n")
	case map[string]any:
		if len(x) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, k := range keys {
			rendered := strings.TrimSpace(renderPlain(x[k]))
			if rendered == "" {
				lines = append(lines, k+":")
				continue
			}
			valueLines := strings.Split(rendered, "\n")
			lines = append(lines, k+": "+valueLines[0])
			for _, line := range valueLines[1:] {
				lines = append(lines, "  "+line)
			}
		}
		return strings.Join(lines, "\n")
	default:
		return fmt.Sprintf("%v", x)
	}
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(s), true
}

func asInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	default:
		return 0, false
	}
}
