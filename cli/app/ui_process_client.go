package app

import (
	"context"
	"errors"
	"strings"

	"builder/server/processview"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
	"github.com/google/uuid"
)

type backgroundUIProcessClient struct {
	manager *shelltool.Manager
	reads   client.ProcessViewClient
	control client.ProcessControlClient
}

func newUIProcessClient(manager *shelltool.Manager) clientui.ProcessClient {
	return newUIProcessClientWithReads(manager, nil, nil)
}

func newUIProcessClientWithReads(manager *shelltool.Manager, reads client.ProcessViewClient, control client.ProcessControlClient) clientui.ProcessClient {
	if manager == nil && reads == nil && control == nil {
		return nil
	}
	return backgroundUIProcessClient{manager: manager, reads: reads, control: control}
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

func (c backgroundUIProcessClient) KillProcess(id string) error {
	id = strings.TrimSpace(id)
	var lastErr error
	if c.control != nil {
		_, err := c.control.KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: uuid.NewString(), ProcessID: id})
		if err == nil {
			return nil
		}
		lastErr = err
	}
	if c.manager == nil {
		if lastErr != nil {
			return lastErr
		}
		return errors.New("background process manager is unavailable")
	}
	return c.manager.Kill(id)
}

func (c backgroundUIProcessClient) InlineOutput(id string, maxChars int) (string, string, error) {
	id = strings.TrimSpace(id)
	var lastErr error
	if c.control != nil {
		resp, err := c.control.GetInlineOutput(context.Background(), serverapi.ProcessInlineOutputRequest{ProcessID: id, MaxChars: maxChars})
		if err == nil {
			return resp.Output, resp.LogPath, nil
		}
		lastErr = err
	}
	if c.manager == nil {
		if lastErr != nil {
			return "", "", lastErr
		}
		return "", "", errors.New("background process manager is unavailable")
	}
	return c.manager.InlineOutput(id, maxChars)
}
