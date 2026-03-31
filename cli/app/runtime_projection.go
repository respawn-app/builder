package app

import (
	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/shared/clientui"
)

func projectRuntimeEvent(evt runtime.Event) clientui.Event {
	return runtimeview.EventFromRuntime(evt)
}

func projectChatSnapshot(snapshot runtime.ChatSnapshot) clientui.ChatSnapshot {
	return runtimeview.ChatSnapshotFromRuntime(snapshot)
}

func projectedRuntimeEventMsg(evt runtime.Event) runtimeEventMsg {
	return runtimeEventMsg{event: projectRuntimeEvent(evt)}
}

func projectRuntimeEventChannel(src <-chan runtime.Event) <-chan clientui.Event {
	if src == nil {
		return nil
	}
	out := make(chan clientui.Event, cap(src))
	go func() {
		defer close(out)
		for evt := range src {
			out <- projectRuntimeEvent(evt)
		}
	}()
	return out
}
