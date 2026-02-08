package app

import (
	"sync/atomic"

	"builder/internal/runtime"
)

type runtimeEventBridge struct {
	ch      chan runtime.Event
	dropped atomic.Uint64
	onDrop  func(total uint64, evt runtime.Event)
}

func newRuntimeEventBridge(buffer int, onDrop func(total uint64, evt runtime.Event)) *runtimeEventBridge {
	if buffer <= 0 {
		buffer = 1
	}
	return &runtimeEventBridge{
		ch:     make(chan runtime.Event, buffer),
		onDrop: onDrop,
	}
}

func (b *runtimeEventBridge) Publish(evt runtime.Event) {
	select {
	case b.ch <- evt:
	default:
		total := b.dropped.Add(1)
		if b.onDrop != nil {
			b.onDrop(total, evt)
		}
	}
}

func (b *runtimeEventBridge) Channel() <-chan runtime.Event {
	return b.ch
}

func (b *runtimeEventBridge) Dropped() uint64 {
	return b.dropped.Load()
}
