package toolspec

import "sort"

type ID string

const (
	ToolShell                ID = "shell"
	ToolExecCommand          ID = "exec_command"
	ToolWriteStdin           ID = "write_stdin"
	ToolViewImage            ID = "view_image"
	ToolPatch                ID = "patch"
	ToolAskQuestion          ID = "ask_question"
	ToolTriggerHandoff       ID = "trigger_handoff"
	ToolWebSearch            ID = "web_search"
	ToolMultiToolUseParallel ID = "multi_tool_use_parallel"
)

var catalogIDs = []ID{
	ToolAskQuestion,
	ToolExecCommand,
	ToolMultiToolUseParallel,
	ToolPatch,
	ToolShell,
	ToolTriggerHandoff,
	ToolViewImage,
	ToolWebSearch,
	ToolWriteStdin,
}

var defaultEnabledIDs = []ID{
	ToolAskQuestion,
	ToolExecCommand,
	ToolPatch,
	ToolShell,
	ToolViewImage,
	ToolWebSearch,
	ToolWriteStdin,
}

var parseAliases = map[string]ID{
	"ask_question":            ToolAskQuestion,
	"bash":                    ToolShell,
	"bash_command":            ToolShell,
	"exec_command":            ToolExecCommand,
	"multi_tool_use_parallel": ToolMultiToolUseParallel,
	"parallel":                ToolMultiToolUseParallel,
	"patch":                   ToolPatch,
	"read_image":              ToolViewImage,
	"shell":                   ToolShell,
	"shell_command":           ToolShell,
	"trigger_handoff":         ToolTriggerHandoff,
	"view_image":              ToolViewImage,
	"web_search":              ToolWebSearch,
	"write_stdin":             ToolWriteStdin,
}

func init() {
	sort.Slice(catalogIDs, func(i, j int) bool { return catalogIDs[i] < catalogIDs[j] })
	sort.Slice(defaultEnabledIDs, func(i, j int) bool { return defaultEnabledIDs[i] < defaultEnabledIDs[j] })
}

func ParseID(v string) (ID, bool) {
	id, ok := parseAliases[v]
	return id, ok
}

func CatalogIDs() []ID {
	out := make([]ID, len(catalogIDs))
	copy(out, catalogIDs)
	return out
}

func DefaultEnabledToolIDs() []ID {
	out := make([]ID, len(defaultEnabledIDs))
	copy(out, defaultEnabledIDs)
	return out
}
