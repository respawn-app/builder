package commands

import (
	"fmt"
	"strings"
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

type Registry struct {
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: map[string]Handler{}}
}

func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register("exit", func(string) Result {
		return Result{Handled: true, Action: ActionExit}
	})
	r.Register("new", func(string) Result {
		return Result{Handled: true, Action: ActionNew}
	})
	r.Register("logout", func(string) Result {
		return Result{Handled: true, Action: ActionLogout}
	})
	r.Register("compact", func(string) Result {
		return Result{Handled: true, Action: ActionCompact}
	})
	return r
}

func (r *Registry) Register(name string, h Handler) {
	if r == nil || h == nil {
		return
	}
	k := strings.ToLower(strings.TrimSpace(name))
	if k == "" {
		return
	}
	r.handlers[k] = h
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
		return Result{Handled: true, Action: ActionNone, Text: "system: missing slash command name"}
	}
	h, exists := r.handlers[name]
	if !exists {
		return Result{Handled: true, Action: ActionNone, Text: fmt.Sprintf("system: unknown command /%s", name)}
	}
	res := h(args)
	res.Handled = true
	return res
}
