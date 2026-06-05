package app

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	previousDuration := transientStatusDuration
	transientStatusDuration = 30 * time.Millisecond
	code := m.Run()
	transientStatusDuration = previousDuration
	os.Exit(code)
}
