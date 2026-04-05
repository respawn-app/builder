package app

type uiActivity uint8

const (
	uiActivityIdle uiActivity = iota
	uiActivityRunning
	uiActivityQueued
	uiActivityQuestion
	uiActivityInterrupted
	uiActivityError
)
