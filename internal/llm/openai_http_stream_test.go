package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type staticAuthHeader struct{}

func (staticAuthHeader) AuthorizationHeader(context.Context) (string, error) {
	return "Bearer test", nil
}

func TestGenerateStream_EmitsAssistantDeltasAndToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"name\":\"shell\",\"call_id\":\"call_1\",\"arguments\":\"\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_1\",\"delta\":\"{\\\"command\\\":\\\"pwd\\\"}\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]},{\"type\":\"function_call\",\"id\":\"fc_1\",\"name\":\"shell\",\"call_id\":\"call_1\",\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	transport := NewHTTPTransport(staticAuthHeader{})
	transport.BaseURL = server.URL
	transport.Client = server.Client()

	var deltas []string
	resp, err := transport.GenerateStream(context.Background(), OpenAIRequest{Model: "gpt-5"}, func(text string) {
		deltas = append(deltas, text)
	})
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	if strings.Join(deltas, "") != "Hello" {
		t.Fatalf("unexpected deltas: %+v", deltas)
	}
	if resp.AssistantText != "Hello" {
		t.Fatalf("unexpected assistant text: %q", resp.AssistantText)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call_1" || resp.ToolCalls[0].Name != "shell" {
		t.Fatalf("unexpected tool call: %+v", resp.ToolCalls[0])
	}
	if string(resp.ToolCalls[0].Input) != "{\"command\":\"pwd\"}" {
		t.Fatalf("unexpected tool args: %s", string(resp.ToolCalls[0].Input))
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}
