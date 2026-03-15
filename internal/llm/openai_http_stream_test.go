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
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"item_id\":\"rs_1\",\"output_index\":1,\"summary_index\":0,\"delta\":\"Plan\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"input_tokens_details\":{\"cached_tokens\":4},\"output_tokens\":7,\"output_tokens_details\":{\"reasoning_tokens\":2},\"total_tokens\":18},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"commentary\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]},{\"type\":\"reasoning\",\"id\":\"rs_1\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"Plan\"}],\"content\":[{\"type\":\"reasoning_text\",\"text\":\"internal trace\"}],\"encrypted_content\":\"enc_1\"},{\"type\":\"function_call\",\"id\":\"fc_1\",\"name\":\"shell\",\"call_id\":\"call_1\",\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	transport := NewHTTPTransport(staticAuthHeader{})
	transport.BaseURL = server.URL
	transport.ProviderMetadata = ResolveOpenAIProviderMetadata(transport.BaseURL)
	transport.Client = server.Client()

	var deltas []string
	var reasoning []ReasoningSummaryDelta
	resp, err := transport.GenerateStreamWithEvents(context.Background(), OpenAIRequest{Model: "gpt-5"}, StreamCallbacks{
		OnAssistantDelta: func(text string) {
			deltas = append(deltas, text)
		},
		OnReasoningSummaryDelta: func(delta ReasoningSummaryDelta) {
			reasoning = append(reasoning, delta)
		},
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
	if resp.AssistantPhase != MessagePhaseCommentary {
		t.Fatalf("unexpected assistant phase: %q", resp.AssistantPhase)
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
	if !resp.Usage.HasCachedInputTokens || resp.Usage.CachedInputTokens != 4 {
		t.Fatalf("unexpected cached usage details: %+v", resp.Usage)
	}
	if len(resp.Reasoning) != 1 || resp.Reasoning[0].Role != "reasoning" || resp.Reasoning[0].Text != "Plan" {
		t.Fatalf("unexpected reasoning summary entries: %+v", resp.Reasoning)
	}
	if len(resp.ReasoningItems) != 1 || resp.ReasoningItems[0].ID != "rs_1" || resp.ReasoningItems[0].EncryptedContent != "enc_1" {
		t.Fatalf("unexpected reasoning items: %+v", resp.ReasoningItems)
	}
	if len(reasoning) != 1 || reasoning[0].Key == "" || reasoning[0].Role != "reasoning" || reasoning[0].Text != "Plan" {
		t.Fatalf("unexpected reasoning delta callbacks: %+v", reasoning)
	}
}

func TestGenerateStream_PreservesBoldReasoningTextWithoutInferringStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"item_id\":\"rs_1\",\"output_index\":0,\"summary_index\":0,\"delta\":\"**Preparing patch**\\n\\nPlain summary text\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2},\"output\":[{\"type\":\"reasoning\",\"id\":\"rs_1\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"**Preparing patch**\\n\\nPlain summary text\"}],\"encrypted_content\":\"enc_1\"}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	transport := NewHTTPTransport(staticAuthHeader{})
	transport.BaseURL = server.URL
	transport.ProviderMetadata = ResolveOpenAIProviderMetadata(transport.BaseURL)
	transport.Client = server.Client()

	var reasoning []ReasoningSummaryDelta
	resp, err := transport.GenerateStreamWithEvents(context.Background(), OpenAIRequest{Model: "gpt-5"}, StreamCallbacks{
		OnReasoningSummaryDelta: func(delta ReasoningSummaryDelta) {
			reasoning = append(reasoning, delta)
		},
	})
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	if len(reasoning) != 1 {
		t.Fatalf("expected 1 reasoning delta callback, got %+v", reasoning)
	}
	if reasoning[0].Text != "**Preparing patch**\n\nPlain summary text" {
		t.Fatalf("summary = %q", reasoning[0].Text)
	}
	if len(resp.Reasoning) != 1 || resp.Reasoning[0].Text != "**Preparing patch**\n\nPlain summary text" {
		t.Fatalf("unexpected final reasoning summary entries: %+v", resp.Reasoning)
	}
}
