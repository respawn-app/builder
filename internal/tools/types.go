package tools

import (
	"context"
	"encoding/json"
)

type Call struct {
	ID     string
	Name   string
	Input  json.RawMessage
	StepID string
}

type Result struct {
	CallID  string          `json:"call_id"`
	Name    string          `json:"name"`
	Output  json.RawMessage `json:"output"`
	IsError bool            `json:"is_error"`
}

type Definition struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type Handler interface {
	Name() string
	Definition() Definition
	Call(ctx context.Context, c Call) (Result, error)
}

type Registry struct {
	byName map[string]Handler
	order  []string
}

func NewRegistry(handlers ...Handler) *Registry {
	m := make(map[string]Handler, len(handlers))
	order := make([]string, 0, len(handlers))
	for _, h := range handlers {
		m[h.Name()] = h
		order = append(order, h.Name())
	}
	return &Registry{byName: m, order: order}
}

func (r *Registry) Get(name string) (Handler, bool) {
	h, ok := r.byName[name]
	return h, ok
}

func (r *Registry) Definitions() []Definition {
	out := make([]Definition, 0, len(r.byName))
	for _, name := range r.order {
		h := r.byName[name]
		out = append(out, h.Definition())
	}
	return out
}
