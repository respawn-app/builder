package app

import (
	shelltool "builder/server/tools/shell"
	"builder/shared/clientui"
)

type backgroundUIProcessClient struct {
	manager *shelltool.Manager
}

func newUIProcessClient(manager *shelltool.Manager) clientui.ProcessClient {
	if manager == nil {
		return nil
	}
	return backgroundUIProcessClient{manager: manager}
}

func (m *uiModel) listProcesses() []clientui.BackgroundProcess {
	if m == nil || m.processClient == nil {
		return nil
	}
	return m.processClient.ListProcesses()
}

func (c backgroundUIProcessClient) ListProcesses() []clientui.BackgroundProcess {
	entries := c.manager.List()
	out := make([]clientui.BackgroundProcess, 0, len(entries))
	for _, entry := range entries {
		out = append(out, projectBackgroundProcess(entry))
	}
	return out
}

func projectBackgroundProcess(entry shelltool.Snapshot) clientui.BackgroundProcess {
	return clientui.BackgroundProcess{
		ID:             entry.ID,
		OwnerSessionID: entry.OwnerSessionID,
		State:          entry.State,
		Command:        entry.Command,
		Workdir:        entry.Workdir,
		StartedAt:      entry.StartedAt,
		FinishedAt:     entry.FinishedAt,
		ExitCode:       cloneInt(entry.ExitCode),
		LogPath:        entry.LogPath,
		RecentOutput:   entry.RecentOutput,
		Running:        entry.Running,
		StdinOpen:      entry.StdinOpen,
		Backgrounded:   entry.Backgrounded,
		KillRequested:  entry.KillRequested,
		LastUpdatedAt:  entry.LastUpdatedAt,
	}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
