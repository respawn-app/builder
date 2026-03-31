package runprompt

import (
	"sync/atomic"

	"builder/server/runtime"
)

type RuntimeEventBridge struct {
	ch      chan runtime.Event
	dropped atomic.Uint64
	onDrop  func(total uint64, evt runtime.Event)
}

func NewRuntimeEventBridge(buffer int, onDrop func(total uint64, evt runtime.Event)) *RuntimeEventBridge {
	if buffer <= 0 {
		buffer = 1
	}
	return &RuntimeEventBridge{ch: make(chan runtime.Event, buffer), onDrop: onDrop}
}

func (b *RuntimeEventBridge) Publish(evt runtime.Event) {
	select {
	case b.ch <- evt:
	default:
		total := b.dropped.Add(1)
		if b.onDrop != nil {
			b.onDrop(total, evt)
		}
	}
}

func (b *RuntimeEventBridge) Channel() <-chan runtime.Event { return b.ch }

func (b *RuntimeEventBridge) Dropped() uint64 { return b.dropped.Load() }
