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
