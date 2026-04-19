package tools

import (
	"builder/shared/toolspec"
	"context"
	"encoding/json"
	"fmt"
)

type Call struct {
	ID     string
	Name   toolspec.ID
	Input  json.RawMessage
	RunID  string
	StepID string
}

type Result struct {
	CallID  string          `json:"call_id"`
	Name    toolspec.ID     `json:"name"`
	Output  json.RawMessage `json:"output"`
	IsError bool            `json:"is_error"`
}

type Definition struct {
	ID          toolspec.ID
	Description string
	Schema      json.RawMessage
	contract    Contract
}

type Handler interface {
	Name() toolspec.ID
	Call(ctx context.Context, c Call) (Result, error)
}

type Registry struct {
	byName map[toolspec.ID]Handler
	order  []toolspec.ID
}

func NewRegistry(handlers ...Handler) *Registry {
	m := make(map[toolspec.ID]Handler, len(handlers))
	order := make([]toolspec.ID, 0, len(handlers))
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

func (r *Registry) Get(name toolspec.ID) (Handler, bool) {
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
