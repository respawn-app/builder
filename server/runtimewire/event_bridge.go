package runtimewire

import (
	"sync/atomic"

	"builder/server/runtime"
)

type EventBridge struct {
	ch      chan runtime.Event
	dropped atomic.Uint64
	onDrop  func(total uint64, evt runtime.Event)
}

func NewEventBridge(buffer int, onDrop func(total uint64, evt runtime.Event)) *EventBridge {
	if buffer <= 0 {
		buffer = 1
	}
	return &EventBridge{ch: make(chan runtime.Event, buffer), onDrop: onDrop}
}

func (b *EventBridge) Publish(evt runtime.Event) {
	select {
	case b.ch <- evt:
	default:
		total := b.dropped.Add(1)
		if b.onDrop != nil {
			b.onDrop(total, evt)
		}
	}
}

func (b *EventBridge) Channel() <-chan runtime.Event { return b.ch }

func (b *EventBridge) Dropped() uint64 { return b.dropped.Load() }
