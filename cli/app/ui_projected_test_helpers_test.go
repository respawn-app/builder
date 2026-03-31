package app

import (
	"builder/server/runtime"
	"builder/shared/clientui"
)

func closedRuntimeEvents() <-chan runtime.Event {
	ch := make(chan runtime.Event)
	close(ch)
	return ch
}

func closedProjectedRuntimeEvents() <-chan clientui.Event {
	ch := make(chan clientui.Event)
	close(ch)
	return ch
}

func newProjectedTestUIModel(runtimeClient clientui.RuntimeClient, runtimeEvents <-chan clientui.Event, askEvents <-chan askEvent, opts ...UIOption) *uiModel {
	if runtimeEvents == nil {
		runtimeEvents = make(chan clientui.Event)
	}
	if askEvents == nil {
		askEvents = make(chan askEvent)
	}
	return NewProjectedUIModel(runtimeClient, runtimeEvents, askEvents, opts...).(*uiModel)
}

func newProjectedStaticUIModel(opts ...UIOption) *uiModel {
	return newProjectedTestUIModel(nil, nil, nil, opts...)
}

func newProjectedEngineUIModel(engine *runtime.Engine, opts ...UIOption) *uiModel {
	return newProjectedTestUIModel(newUIRuntimeClient(engine), nil, nil, opts...)
}
