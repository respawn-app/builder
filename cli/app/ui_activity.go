package app

type uiActivity uint8

const (
	uiActivityIdle uiActivity = iota
	uiActivityRunning
	uiActivityQuestion
	uiActivityInterrupted
	uiActivityError
)
