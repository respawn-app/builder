package llm

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type providerTestAuth struct{}

func (providerTestAuth) AuthorizationHeader(context.Context) (string, error) {
	return "Bearer test", nil
}

func TestInferProviderFromModel(t *testing.T) {
	if got := InferProviderFromModel("gpt-5"); got != ProviderOpenAI {
		t.Fatalf("expected openai provider, got %q", got)
	}
	if got := InferProviderFromModel("claude-3-7-sonnet"); got != ProviderAnthropic {
		t.Fatalf("expected anthropic provider, got %q", got)
	}
}

func TestNewProviderClient_OpenAI(t *testing.T) {
	httpClient := &http.Client{Timeout: 7 * time.Second}
	client, err := NewProviderClient(ProviderClientOptions{
		Model:          "gpt-5.3-codex",
		Auth:           providerTestAuth{},
		HTTPClient:     httpClient,
		ModelVerbosity: "HIGH",
	})
	if err != nil {
		t.Fatalf("new provider client: %v", err)
	}
	openAIClient, ok := client.(*OpenAIClient)
	if !ok {
		t.Fatalf("expected *OpenAIClient, got %T", client)
	}
	transport, ok := openAIClient.transport.(*HTTPTransport)
	if !ok {
		t.Fatalf("expected *HTTPTransport, got %T", openAIClient.transport)
	}
	if transport.Client != httpClient {
		t.Fatal("expected provider HTTP client override to be used")
	}
	if transport.ContextWindowTokens != 400_000 {
		t.Fatalf("expected context window from model metadata, got %d", transport.ContextWindowTokens)
	}
	if transport.ModelVerbosity != "high" {
		t.Fatalf("expected normalized model verbosity, got %q", transport.ModelVerbosity)
	}
}

func TestNewProviderClient_AnthropicNotImplemented(t *testing.T) {
	_, err := NewProviderClient(ProviderClientOptions{
		Model: "claude-3-7-sonnet",
		Auth:  providerTestAuth{},
	})
	if !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}

func TestProviderErrorReducerForUnknownIDFailsFast(t *testing.T) {
	_, err := providerErrorReducerForID("custom-provider-id")
	if err == nil {
		t.Fatal("expected missing provider reducer error")
	}
}
