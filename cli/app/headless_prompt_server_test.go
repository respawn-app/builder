package app

import "builder/shared/client"

func newHeadlessRunPromptClient(server *embeddedAppServer) client.RunPromptClient {
	target, err := runPromptTargetForEmbeddedAttachment(server)
	if err != nil {
		panic(err)
	}
	return target.Value.Client
}
