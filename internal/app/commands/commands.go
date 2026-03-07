package commands

import (
	"builder/prompts"
	"sort"
	"strings"
	"unicode"
)

type Action string

const (
	ActionNone              Action = "none"
	ActionExit              Action = "exit"
	ActionNew               Action = "new"
	ActionResume            Action = "resume"
	ActionLogout            Action = "logout"
	ActionCompact           Action = "compact"
	ActionSetName           Action = "set_name"
	ActionSetThinking       Action = "set_thinking"
	ActionSetSupervisor     Action = "set_supervisor"
	ActionSetAutoCompaction Action = "set_auto_compaction"
	ActionProcesses         Action = "processes"
	ActionBack              Action = "back"
	ActionUnhandled         Action = "unhandled"
)

type Result struct {
	Handled            bool
	Action             Action
	Text               string
	Args               string
	SubmitUser         bool
	User               string
	FreshConversation  bool
	SessionName        string
	ThinkingLevel      string
	SupervisorMode     string
	AutoCompactionMode string
}

type Handler func(args string) Result

type Command struct {
	Name         string
	Description  string
	RunWhileBusy bool
}

type registeredCommand struct {
	command Command
	handler Handler
}

type Registry struct {
	handlers map[string]registeredCommand
}

func NewRegistry() *Registry {
	return &Registry{handlers: map[string]registeredCommand{}}
}

func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register("exit", "Exit builder", func(string) Result {
		return Result{Handled: true, Action: ActionExit}
	})
	r.Register("new", "Create a new session", func(string) Result {
		return Result{Handled: true, Action: ActionNew}
	})
	r.Register("resume", "Go to startup screen (session picker)", func(string) Result {
		return Result{Handled: true, Action: ActionResume}
	})
	r.Register("logout", "Log out and re-authenticate", func(string) Result {
		return Result{Handled: true, Action: ActionLogout}
	})
	r.Register("compact", "Compact the current context (optional: /compact <instructions>)", func(args string) Result {
		return Result{Handled: true, Action: ActionCompact, Args: strings.TrimSpace(args)}
	})
	r.RegisterWithOptions("name", "Set session title and terminal title (usage: /name <title>; empty resets)", RegisterOptions{RunWhileBusy: true}, func(args string) Result {
		return Result{Handled: true, Action: ActionSetName, SessionName: strings.TrimSpace(args)}
	})
	r.RegisterWithOptions("thinking", "Set or show thinking level (usage: /thinking <low|medium|high|xhigh>; empty shows current)", RegisterOptions{RunWhileBusy: true}, func(args string) Result {
		return Result{Handled: true, Action: ActionSetThinking, ThinkingLevel: strings.ToLower(strings.TrimSpace(args))}
	})
	r.RegisterWithOptions("supervisor", "Toggle reviewer invocation (usage: /supervisor [on|off]; empty toggles)", RegisterOptions{RunWhileBusy: true}, func(args string) Result {
		return Result{Handled: true, Action: ActionSetSupervisor, SupervisorMode: strings.ToLower(strings.TrimSpace(args))}
	})
	r.RegisterWithOptions("autocompaction", "Toggle auto-compaction (usage: /autocompaction [on|off]; empty toggles)", RegisterOptions{RunWhileBusy: true}, func(args string) Result {
		return Result{Handled: true, Action: ActionSetAutoCompaction, AutoCompactionMode: strings.ToLower(strings.TrimSpace(args))}
	})
	r.RegisterWithOptions("ps", "List background processes or manage one (usage: /ps [kill|inline|editor|open] <id>)", RegisterOptions{RunWhileBusy: true}, func(args string) Result {
		return Result{Handled: true, Action: ActionProcesses, Args: strings.TrimSpace(args)}
	})
	r.Register("back", "Jump to parent session if current session was spawned from another", func(string) Result {
		return Result{Handled: true, Action: ActionBack}
	})
	registerPromptCommands(r, []promptCommandSpec{
		{
			Name:          "review",
			Description:   "Run code review (optional: /review <what to review>)",
			Prompt:        prompts.ReviewPrompt,
			AppendRawArgs: true,
			FreshSession:  true,
		},
		{
			Name:          "init",
			Description:   "Run repository initialization prompt (optional: /init <instructions>)",
			Prompt:        prompts.InitPrompt,
			AppendRawArgs: true,
			FreshSession:  true,
		},
	})
	return r
}

type RegisterOptions struct {
	RunWhileBusy bool
}

func (r *Registry) Register(name string, description string, h Handler) {
	r.RegisterWithOptions(name, description, RegisterOptions{}, h)
}

func (r *Registry) RegisterWithOptions(name string, description string, options RegisterOptions, h Handler) {
	if r == nil || h == nil {
		return
	}
	k := strings.ToLower(strings.TrimSpace(name))
	if k == "" {
		return
	}
	if strings.IndexFunc(k, unicode.IsSpace) >= 0 {
		panic("slash command names must not contain whitespace")
	}
	r.handlers[k] = registeredCommand{
		command: Command{Name: k, Description: strings.TrimSpace(description), RunWhileBusy: options.RunWhileBusy},
		handler: h,
	}
}

func (r *Registry) Commands() []Command {
	if r == nil {
		return nil
	}
	list := make([]Command, 0, len(r.handlers))
	for _, entry := range r.handlers {
		list = append(list, entry.command)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

func (r *Registry) Match(query string) []Command {
	if r == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(query))
	type scoredCommand struct {
		command Command
		index   int
	}
	scored := make([]scoredCommand, 0, len(r.handlers))
	for _, entry := range r.handlers {
		idx := 0
		if normalized != "" {
			idx = strings.Index(entry.command.Name, normalized)
			if idx < 0 {
				continue
			}
		}
		scored = append(scored, scoredCommand{command: entry.command, index: idx})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].index != scored[j].index {
			return scored[i].index < scored[j].index
		}
		if len(scored[i].command.Name) != len(scored[j].command.Name) {
			return len(scored[i].command.Name) < len(scored[j].command.Name)
		}
		return scored[i].command.Name < scored[j].command.Name
	})
	out := make([]Command, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.command)
	}
	return out
}

func (r *Registry) Parse(raw string) (name string, args string, isCommand bool) {
	if r == nil {
		return "", "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed[0] != '/' {
		return "", "", false
	}
	payload := strings.TrimSpace(trimmed[1:])
	if payload == "" {
		return "", "", true
	}
	parts := strings.Fields(payload)
	name = strings.ToLower(parts[0])
	if len(parts) > 1 {
		args = strings.TrimSpace(strings.TrimPrefix(payload, parts[0]))
	}
	return name, args, true
}

func (r *Registry) Execute(raw string) Result {
	name, args, ok := r.Parse(raw)
	if !ok {
		return Result{Handled: false, Action: ActionUnhandled}
	}
	if name == "" {
		return Result{Handled: false, Action: ActionUnhandled}
	}
	registered, exists := r.handlers[name]
	if !exists {
		return Result{Handled: false, Action: ActionUnhandled}
	}
	res := registered.handler(args)
	res.Handled = true
	return res
}

func (r *Registry) Command(raw string) (Command, bool) {
	name, _, ok := r.Parse(raw)
	if !ok || name == "" {
		return Command{}, false
	}
	registered, exists := r.handlers[name]
	if !exists {
		return Command{}, false
	}
	return registered.command, true
}
