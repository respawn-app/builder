package tools

import "encoding/json"

func ErrorResult(c Call, msg string) Result {
	return ErrorResultWith(c, msg, defaultMarshal)
}

func ErrorResultWith(c Call, msg string, marshal func(any) (json.RawMessage, error)) Result {
	if marshal == nil {
		marshal = defaultMarshal
	}
	body, err := marshal(map[string]any{"error": msg})
	if err != nil {
		body, _ = defaultMarshal(map[string]any{"error": msg})
	}
	return Result{CallID: c.ID, Name: c.Name, Output: body, IsError: true, Summary: msg}
}

func defaultMarshal(v any) (json.RawMessage, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return body, nil
}
