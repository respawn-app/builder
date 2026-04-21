package toolspec

import (
	"sort"
	"strings"
)

type ID string

const (
	ToolExecCommand    ID = "exec_command"
	ToolWriteStdin     ID = "write_stdin"
	ToolViewImage      ID = "view_image"
	ToolPatch          ID = "patch"
	ToolAskQuestion    ID = "ask_question"
	ToolTriggerHandoff ID = "trigger_handoff"
	ToolWebSearch      ID = "web_search"
)

var catalogIDs = []ID{
	ToolAskQuestion,
	ToolExecCommand,
	ToolPatch,
	ToolTriggerHandoff,
	ToolViewImage,
	ToolWebSearch,
	ToolWriteStdin,
}

var defaultEnabledIDs = []ID{
	ToolAskQuestion,
	ToolExecCommand,
	ToolPatch,
	ToolViewImage,
	ToolWebSearch,
	ToolWriteStdin,
}

var parseAliases = map[string]ID{
	"ask_question":    ToolAskQuestion,
	"bash":            ToolExecCommand,
	"bash_command":    ToolExecCommand,
	"exec_command":    ToolExecCommand,
	"patch":           ToolPatch,
	"read_image":      ToolViewImage,
	"shell":           ToolExecCommand,
	"shell_command":   ToolExecCommand,
	"trigger_handoff": ToolTriggerHandoff,
	"view_image":      ToolViewImage,
	"web_search":      ToolWebSearch,
	"write_stdin":     ToolWriteStdin,
}

var configAliases = map[string]ID{
	"ask_question":    ToolAskQuestion,
	"exec_command":    ToolExecCommand,
	"patch":           ToolPatch,
	"read_image":      ToolViewImage,
	"shell":           ToolExecCommand,
	"trigger_handoff": ToolTriggerHandoff,
	"view_image":      ToolViewImage,
	"web_search":      ToolWebSearch,
	"write_stdin":     ToolWriteStdin,
}

func init() {
	sort.Slice(catalogIDs, func(i, j int) bool { return catalogIDs[i] < catalogIDs[j] })
	sort.Slice(defaultEnabledIDs, func(i, j int) bool { return defaultEnabledIDs[i] < defaultEnabledIDs[j] })
}

func ParseID(v string) (ID, bool) {
	id, ok := parseAliases[strings.TrimSpace(v)]
	return id, ok
}

func ParseConfigID(v string) (ID, bool) {
	id, ok := configAliases[strings.TrimSpace(v)]
	return id, ok
}

func ConfigName(id ID) string {
	if id == ToolExecCommand {
		return "shell"
	}
	return string(id)
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
