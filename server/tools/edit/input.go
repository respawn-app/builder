package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type input struct {
	Path       string
	OldString  string
	NewString  string
	ReplaceAll bool
}

func parseInput(raw json.RawMessage) (input, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return input{}, failf("expected JSON object input.")
	}
	if fields == nil {
		return input{}, failf("expected JSON object input.")
	}
	path, err := pickString(fields, "path", "file_path", "filePath")
	if err != nil {
		return input{}, err
	}
	oldString, err := pickString(fields, "old_string", "oldString", "oldText")
	if err != nil {
		return input{}, err
	}
	newString, err := pickString(fields, "new_string", "newString", "newText")
	if err != nil {
		return input{}, err
	}
	replaceAll, err := pickBool(fields, "replace_all", "replaceAll")
	if err != nil {
		return input{}, err
	}
	if strings.TrimSpace(path) == "" {
		return input{}, failf("path is required.")
	}
	if oldString == newString {
		return input{}, failf("old_string and new_string must be different.")
	}
	return input{Path: path, OldString: oldString, NewString: newString, ReplaceAll: replaceAll}, nil
}

func pickString(fields map[string]json.RawMessage, names ...string) (string, error) {
	var selected []byte
	found := ""
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", failf("%s must be a string.", name)
		}
		encoded, _ := json.Marshal(value)
		if found != "" && !bytes.Equal(selected, encoded) {
			return "", failf("conflicting aliases for %s.", names[0])
		}
		found = name
		selected = encoded
	}
	if found == "" {
		return "", failf("%s is required.", names[0])
	}
	var value string
	_ = json.Unmarshal(selected, &value)
	return value, nil
}

func pickBool(fields map[string]json.RawMessage, names ...string) (bool, error) {
	var selected []byte
	found := ""
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return false, failf("%s must be a boolean.", name)
		}
		encoded, _ := json.Marshal(value)
		if found != "" && !bytes.Equal(selected, encoded) {
			return false, failf("conflicting aliases for %s.", names[0])
		}
		found = name
		selected = encoded
	}
	if found == "" {
		return false, nil
	}
	var value bool
	if err := json.Unmarshal(selected, &value); err != nil {
		return false, fmt.Errorf("decode %s: %w", names[0], err)
	}
	return value, nil
}
