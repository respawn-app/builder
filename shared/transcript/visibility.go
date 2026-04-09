package transcript

import "strings"

type EntryVisibility string

const (
	EntryVisibilityAuto       EntryVisibility = ""
	EntryVisibilityAll        EntryVisibility = "all"
	EntryVisibilityDetailOnly EntryVisibility = "detail_only"
)

func NormalizeEntryVisibility(visibility EntryVisibility) EntryVisibility {
	switch strings.ToLower(strings.TrimSpace(string(visibility))) {
	case "", "auto":
		return EntryVisibilityAuto
	case string(EntryVisibilityAll):
		return EntryVisibilityAll
	case string(EntryVisibilityDetailOnly):
		return EntryVisibilityDetailOnly
	default:
		return EntryVisibility(strings.TrimSpace(string(visibility)))
	}
}
