package app

import (
	"context"

	"builder/server/processview"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type backgroundUIProcessClient struct {
	manager *shelltool.Manager
	reads   client.ProcessViewClient
}

func newUIProcessClient(manager *shelltool.Manager) clientui.ProcessClient {
	return newUIProcessClientWithReads(manager, nil)
}

func newUIProcessClientWithReads(manager *shelltool.Manager, reads client.ProcessViewClient) clientui.ProcessClient {
	if manager == nil && reads == nil {
		return nil
	}
	return backgroundUIProcessClient{manager: manager, reads: reads}
}

func (m *uiModel) listProcesses() []clientui.BackgroundProcess {
	if m == nil || m.processClient == nil {
		return nil
	}
	return m.processClient.ListProcesses()
}

func (c backgroundUIProcessClient) ListProcesses() []clientui.BackgroundProcess {
	if c.reads != nil {
		resp, err := c.reads.ListProcesses(context.Background(), serverapi.ProcessListRequest{})
		if err == nil {
			return resp.Processes
		}
	}
	if c.manager == nil {
		return nil
	}
	entries := c.manager.List()
	out := make([]clientui.BackgroundProcess, 0, len(entries))
	for _, entry := range entries {
		out = append(out, processview.ProcessFromSnapshot(entry))
	}
	return out
}
