package patch

import (
	"regexp"

	patchformat "builder/shared/transcript/patchformat"
)

const hunkMaxFuzz = 8

var unifiedHunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: .*)?$`)

type editHunk struct {
	header  hunkHeader
	changes []patchformat.ChangeLine
}

type hunkHeader struct {
	hasPosition bool
	oldStart    int
	oldCount    int
	newStart    int
	newCount    int
}
