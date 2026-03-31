package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const InvalidWebSearchQueryMessage = "you provided an invalid search query"

type WebSearchInput struct {
	Query          string   `json:"query"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

func ParseWebSearchInput(raw json.RawMessage) (WebSearchInput, error) {
	var in WebSearchInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return WebSearchInput{}, err
	}
	return in, nil
}

func ValidateWebSearchQuery(query string) error {
	if strings.TrimSpace(query) == "" {
		return errors.New(InvalidWebSearchQueryMessage)
	}
	return nil
}

func ValidateWebSearchInput(raw json.RawMessage) error {
	in, err := ParseWebSearchInput(raw)
	if err != nil {
		return errors.New(InvalidWebSearchQueryMessage)
	}
	return ValidateWebSearchQuery(in.Query)
}

func FormatWebSearchDisplayText(query string) string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "web search: invalid query"
	}
	return fmt.Sprintf("web search: %q", trimmed)
}
