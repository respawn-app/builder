package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"builder/internal/app"
)

func main() {
	var (
		workspace = flag.String("workspace", ".", "workspace root")
		sessionID = flag.String("session", "", "session id to resume")
		model     = flag.String("model", "gpt-5", "model name")
	)
	flag.Parse()

	if err := app.Run(context.Background(), app.Options{
		WorkspaceRoot: *workspace,
		SessionID:     *sessionID,
		Model:         *model,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
