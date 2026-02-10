package commands

import (
	"sort"
	"strings"
	"unicode"
)

type Action string

const (
	ActionNone      Action = "none"
	ActionExit      Action = "exit"
	ActionNew       Action = "new"
	ActionLogout    Action = "logout"
	ActionCompact   Action = "compact"
	ActionUnhandled Action = "unhandled"
)

type Result struct {
	Handled bool
	Action  Action
	Text    string
}

type Handler func(args string) Result

type Command struct {
	Name        string
	Description string
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
	r.Register("logout", "Log out and re-authenticate", func(string) Result {
		return Result{Handled: true, Action: ActionLogout}
	})
	r.Register("compact", "Compact the current context", func(string) Result {
		return Result{Handled: true, Action: ActionCompact}
	})
	return r
}

func (r *Registry) Register(name string, description string, h Handler) {
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
		command: Command{Name: k, Description: strings.TrimSpace(description)},
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
