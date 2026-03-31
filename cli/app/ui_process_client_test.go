package app

import (
	"context"
	"testing"
	"time"

	shelltool "builder/server/tools/shell"
)

func TestUIProcessClientProjectsManagerSnapshots(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'done\n'; sleep 0.4; exit 7"},
		DisplayCommand: "project-test",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start background process: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected background process")
	}

	client := newUIProcessClient(manager)
	waitForTestCondition(t, 2*time.Second, "background process to finish", func() bool {
		for _, entry := range client.ListProcesses() {
			if entry.ID == res.SessionID {
				return !entry.Running && entry.ExitCode != nil
			}
		}
		return false
	})

	var projectedExitCode *int
	found := false
	for _, entry := range client.ListProcesses() {
		if entry.ID != res.SessionID {
			continue
		}
		found = true
		if entry.Command != "project-test" {
			t.Fatalf("command = %q, want project-test", entry.Command)
		}
		if entry.Workdir != workdir {
			t.Fatalf("workdir = %q, want %q", entry.Workdir, workdir)
		}
		if entry.LogPath == "" {
			t.Fatal("expected projected log path")
		}
		if entry.ExitCode == nil || *entry.ExitCode != 7 {
			t.Fatalf("exit code = %+v, want 7", entry.ExitCode)
		}
		projectedExitCode = entry.ExitCode
		break
	}
	if !found {
		t.Fatalf("expected projected process entry for %s", res.SessionID)
	}

	*projectedExitCode = 0
	for _, entry := range client.ListProcesses() {
		if entry.ID == res.SessionID {
			if entry.ExitCode == nil || *entry.ExitCode != 7 {
				t.Fatalf("expected projected exit code clone to remain 7, got %+v", entry.ExitCode)
			}
			return
		}
	}
	t.Fatalf("expected projected process entry for %s on second read", res.SessionID)
}
