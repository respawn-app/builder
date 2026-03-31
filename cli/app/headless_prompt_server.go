package app

import (
	"fmt"
	"io"
	"strings"

	"builder/server/launch"
	"builder/server/runprompt"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools/askquestion"
	"builder/shared/client"
	"builder/shared/serverapi"
)

func newHeadlessRunPromptClient(server embeddedServer) client.RunPromptClient {
	return server.RunPromptClient()
}

func ensureSubagentSessionName(store *session.Store) error {
	return launch.EnsureSubagentSessionName(store)
}

func runPromptAskHandler(req askquestion.Request) (askquestion.Response, error) {
	return runprompt.RunPromptAskHandler(req)
}

func writeRunProgressEvent(w io.Writer, evt runtime.Event) {
	publishRunPromptProgress(runPromptIOProgressSink{writer: w}, evt)
}

func publishRunPromptProgress(progress serverapi.RunPromptProgressSink, evt runtime.Event) {
	runprompt.PublishRunPromptProgress(progress, evt)
}

func runPromptProgressFromRuntimeEvent(evt runtime.Event) (serverapi.RunPromptProgress, bool) {
	return runprompt.RunPromptProgressFromRuntimeEvent(evt)
}

type runPromptIOProgressSink struct {
	writer io.Writer
}

func (s runPromptIOProgressSink) PublishRunPromptProgress(progress serverapi.RunPromptProgress) {
	if s.writer == nil {
		return
	}
	message := strings.TrimSpace(progress.Message)
	if message == "" {
		return
	}
	_, _ = fmt.Fprintln(s.writer, message)
}
