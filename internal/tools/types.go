package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

type ID string

const (
	ToolShell                ID = "shell"
	ToolPatch                ID = "patch"
	ToolAskQuestion          ID = "ask_question"
	ToolWebSearch            ID = "web_search"
	ToolMultiToolUseParallel ID = "multi_tool_use_parallel"
)

func ParseID(v string) (ID, bool) {
	return parseCatalogID(v)
}

type Call struct {
	ID     string
	Name   ID
	Input  json.RawMessage
	StepID string
}

type Result struct {
	CallID  string          `json:"call_id"`
	Name    ID              `json:"name"`
	Output  json.RawMessage `json:"output"`
	IsError bool            `json:"is_error"`
}

type Definition struct {
	ID          ID
	Description string
	Schema      json.RawMessage
}

type Handler interface {
	Name() ID
	Call(ctx context.Context, c Call) (Result, error)
}

type Registry struct {
	byName map[ID]Handler
	order  []ID
}

func NewRegistry(handlers ...Handler) *Registry {
	m := make(map[ID]Handler, len(handlers))
	order := make([]ID, 0, len(handlers))
	for _, h := range handlers {
		id := h.Name()
		if _, ok := definitionFor(id); !ok {
			panic(fmt.Sprintf("tool %q is missing centralized definition", id))
		}
		if _, exists := m[id]; exists {
			panic(fmt.Sprintf("duplicate tool handler registration for %q", id))
		}
		m[id] = h
		order = append(order, id)
	}
	return &Registry{byName: m, order: order}
}

func (r *Registry) Get(name ID) (Handler, bool) {
	h, ok := r.byName[name]
	return h, ok
}

func (r *Registry) Definitions() []Definition {
	out := make([]Definition, 0, len(r.byName))
	for _, id := range r.order {
		def, _ := definitionFor(id)
		out = append(out, def)
	}
	return out
}
