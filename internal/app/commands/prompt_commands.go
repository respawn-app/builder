package commands

import "strings"

type promptCommandSpec struct {
	Name          string
	Description   string
	Prompt        string
	AppendRawArgs bool
	FreshSession  bool
}

func registerPromptCommands(r *Registry, specs []promptCommandSpec) {
	if r == nil {
		return
	}
	for _, spec := range specs {
		commandName := spec.Name
		commandDescription := spec.Description
		commandPrompt := spec.Prompt
		appendRawArgs := spec.AppendRawArgs
		freshSession := spec.FreshSession
		r.Register(commandName, commandDescription, func(args string) Result {
			return Result{
				Handled:           true,
				Action:            ActionNone,
				SubmitUser:        true,
				User:              buildPromptSubmission(commandPrompt, args, appendRawArgs),
				FreshConversation: freshSession,
			}
		})
	}
}

func buildPromptSubmission(prompt, args string, appendRawArgs bool) string {
	if !appendRawArgs {
		return prompt
	}
	trimmedArgs := strings.TrimSpace(args)
	if trimmedArgs == "" {
		return prompt
	}
	base := strings.TrimRight(prompt, "\n")
	return base + "\n\n" + trimmedArgs
}
