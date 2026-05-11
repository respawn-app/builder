package runtime

type goalLoopLifecycleState string

const (
	goalLoopLifecycleIdle      goalLoopLifecycleState = "idle"
	goalLoopLifecycleRunning   goalLoopLifecycleState = "running"
	goalLoopLifecycleSuspended goalLoopLifecycleState = "suspended"
)

func (s goalLoopLifecycleState) IsRunning() bool {
	return s == goalLoopLifecycleRunning
}

func (s goalLoopLifecycleState) IsSuspended() bool {
	return s == goalLoopLifecycleSuspended
}
