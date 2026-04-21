package tui

import "builder/shared/transcript"

type transcriptMessageStyle uint8

const (
	transcriptMessageStyleNone transcriptMessageStyle = iota
	transcriptMessageStyleSuccess
	transcriptMessageStyleWarning
	transcriptMessageStyleError
)

func transcriptMessageStyleForRole(role string) transcriptMessageStyle {
	switch transcript.NormalizeEntryRole(role) {
	case "reviewer_status", "reviewer_suggestions":
		return transcriptMessageStyleSuccess
	case "warning", "cache_warning":
		return transcriptMessageStyleWarning
	case "error", roleDeveloperErrorFeedback:
		return transcriptMessageStyleError
	default:
		return transcriptMessageStyleNone
	}
}

func transcriptMessageStyleSymbol(style transcriptMessageStyle) string {
	switch style {
	case transcriptMessageStyleSuccess:
		return "§"
	case transcriptMessageStyleWarning:
		return "⚠"
	case transcriptMessageStyleError:
		return "!"
	default:
		return ""
	}
}
