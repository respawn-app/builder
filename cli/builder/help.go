package main

import (
	"flag"
	"fmt"
	"io"
)

func writeHelpSection(w io.Writer, title string, lines ...string) {
	if w == nil || title == "" {
		return
	}
	_, _ = fmt.Fprintln(w, title)
	for _, line := range lines {
		_, _ = fmt.Fprintln(w, line)
	}
	if len(lines) > 0 {
		_, _ = fmt.Fprintln(w)
	}
}

func writeRootUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	out := fs.Output()
	writeHelpSection(out, "Usage of builder:",
		"  builder [flags]",
		"  builder run [flags] <prompt>",
		"  builder serve [flags]",
		"  builder project [path]",
		"  builder project list",
		"  builder project create --path <server-path> --name <project-name>",
		"  builder attach [--project <project-id>] [path]",
		"  builder rebind <session-id> <new-path>",
	)
	writeHelpSection(out, "What This Does:",
		"  `builder` without a subcommand starts the interactive TUI.",
		"  `builder run` executes one headless prompt and exits.",
		"  `builder serve` starts the app server in daemon mode.",
		"  `builder project` / `attach` / `rebind` inspect or repair workspace bindings.",
	)
	writeHelpSection(out, "Commands:",
		"  run      Execute a headless prompt against a workspace and print the final result.",
		"  serve    Start the Builder app server and keep serving until interrupted.",
		"  project  Inspect project bindings, list projects, or create a project.",
		"  attach   Attach another workspace path to an existing project.",
		"  rebind   Retarget one session to a different workspace root.",
	)
	writeHelpSection(out, "Examples:",
		"  builder",
		"  builder run --fast \"summarize the repo\"",
		"  builder project",
		"  builder attach ../other-checkout",
		"  builder rebind <session-id> ../moved-workspace",
		"  builder <command> --help",
	)
	writeHelpSection(out, "Flags:")
	fs.PrintDefaults()
}

func writeRunUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	out := fs.Output()
	writeHelpSection(out, "Usage of builder run:",
		"  builder run [flags] <prompt>",
	)
	writeHelpSection(out, "What This Does:",
		"  Execute one headless prompt against a workspace without starting the TUI.",
		"  Builder creates or resumes a session, runs the prompt, prints the final answer, and exits.",
	)
	writeHelpSection(out, "Session Selection:",
		"  `--session <id>` resumes an existing session.",
		"  `--continue <id>` is the same concept, optimized for chaining follow-up runs.",
		"  `--session` and `--continue` may both be provided only when they match.",
	)
	writeHelpSection(out, "Subagents:",
		"  `--agent <role>` selects a named role from `[subagents.<role>]` in `~/.builder/config.toml`.",
		"  `--fast` is shorthand for the built-in `fast` role.",
	)
	writeHelpSection(out, "Examples:",
		"  builder run \"summarize the unstaged changes\"",
		"  builder run --continue <session-id> \"follow-up\"",
		"  builder run --fast --output-mode=json \"scan the repo and return JSON\"",
		"  builder run --model gpt-5.4-mini \"review this module\"",
	)
	writeHelpSection(out, "Flags:")
	fs.PrintDefaults()
}

func writeProjectUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	out := fs.Output()
	writeHelpSection(out, "Usage of builder project:",
		"  builder project [path]",
		"  builder project list",
		"  builder project create --path <server-path> --name <project-name>",
	)
	writeHelpSection(out, "What This Does:",
		"  Inspect or manage Builder project bindings.",
		"  `builder project [path]` prints the project id bound to `path` or the current directory.",
		"  `builder project list` lists projects known to the current server.",
		"  `builder project create` registers a new project for a server-visible workspace path.",
	)
	writeHelpSection(out, "Path Semantics:",
		"  For local loopback mode, paths are local filesystem paths.",
		"  For remote daemons, paths passed to `project create` must be visible on the server machine.",
	)
	writeHelpSection(out, "Examples:",
		"  builder project",
		"  builder project ../other-checkout",
		"  builder project list",
		"  builder project create --path /srv/repos/app --name app",
	)
}

func writeProjectListUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	out := fs.Output()
	writeHelpSection(out, "Usage of builder project list:",
		"  builder project list",
	)
	writeHelpSection(out, "What This Does:",
		"  List projects known to the current Builder server.",
		"  Output columns are: project id, display name, and root path.",
	)
	writeHelpSection(out, "Examples:",
		"  builder project list",
	)
}

func writeProjectCreateUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	out := fs.Output()
	writeHelpSection(out, "Usage of builder project create:",
		"  builder project create --path <server-path> --name <project-name>",
	)
	writeHelpSection(out, "What This Does:",
		"  Create a new Builder project and bind its first workspace root.",
		"  The path must exist and must be visible to the Builder server.",
	)
	writeHelpSection(out, "Examples:",
		"  builder project create --path /srv/repos/app --name app",
	)
	writeHelpSection(out, "Flags:")
	fs.PrintDefaults()
}

func writeAttachUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	out := fs.Output()
	writeHelpSection(out, "Usage of builder attach:",
		"  builder attach [path]",
		"  builder attach --project <project-id> [path]",
	)
	writeHelpSection(out, "What This Does:",
		"  Attach another workspace path to an existing Builder project.",
		"  The command prints the project id after the attach succeeds.",
	)
	writeHelpSection(out, "How Project Selection Works:",
		"  Without `--project`, Builder reads the project bound to the current working directory and reuses it.",
		"  With `--project`, Builder skips current-directory lookup and uses the explicit project id instead.",
		"  If `[path]` is omitted, Builder attaches the current directory.",
	)
	writeHelpSection(out, "Path Semantics:",
		"  In loopback mode, `[path]` is a local filesystem path.",
		"  Against a remote daemon, `[path]` must be visible on the server machine.",
	)
	writeHelpSection(out, "Examples:",
		"  builder attach ../other-checkout",
		"  builder attach --project <project-id> /srv/repos/other-checkout",
	)
	writeHelpSection(out, "Flags:")
	fs.PrintDefaults()
}

func writeRebindUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	out := fs.Output()
	writeHelpSection(out, "Usage of builder rebind:",
		"  builder rebind <session-id> <new-path>",
	)
	writeHelpSection(out, "What This Does:",
		"  Retarget one session to a different workspace root.",
		"  Use this when the original workspace moved or when the session should continue from another bound copy.",
	)
	writeHelpSection(out, "Requirements:",
		"  `<new-path>` must exist.",
		"  `<new-path>` must not already be bound to a different project.",
	)
	writeHelpSection(out, "Examples:",
		"  builder rebind <session-id> ../moved-workspace",
	)
}

func writeServeUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	out := fs.Output()
	writeHelpSection(out, "Usage of builder serve:",
		"  builder serve [flags]",
	)
	writeHelpSection(out, "What This Does:",
		"  Start the Builder app server and keep serving until interrupted.",
		"  This is for daemon/server mode, not for running one prompt interactively.",
	)
	writeHelpSection(out, "Notes:",
		"  `builder serve` is workspace-agnostic at startup.",
		"  Session config is resolved from each session workspace: defaults, ~/.builder, env, <workspace>/.builder.",
	)
	writeHelpSection(out, "Examples:",
		"  builder serve",
		"  builder project create --path /srv/repos/app --name app",
	)
	writeHelpSection(out, "Flags:")
	fs.PrintDefaults()
}
