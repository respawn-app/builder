package runtimeview

import (
	"builder/server/runtime"
	"builder/shared/clientui"
)

// Reader exposes server-owned active-session hydration views for loopback clients.
type Reader interface {
	MainView() clientui.RuntimeMainView
	Status() clientui.RuntimeStatus
	SessionView() clientui.RuntimeSessionView
}

type engineReader struct {
	engine *runtime.Engine
}

func NewReader(engine *runtime.Engine) Reader {
	if engine == nil {
		return nil
	}
	return engineReader{engine: engine}
}

func (r engineReader) MainView() clientui.RuntimeMainView {
	return MainViewFromRuntime(r.engine)
}

func (r engineReader) Status() clientui.RuntimeStatus {
	return StatusFromRuntime(r.engine)
}

func (r engineReader) SessionView() clientui.RuntimeSessionView {
	return SessionViewFromRuntime(r.engine)
}
