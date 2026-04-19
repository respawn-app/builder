package llm

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPClientSharesTransport(t *testing.T) {
	first := NewHTTPClient(5 * time.Second)
	second := NewHTTPClient(10 * time.Second)

	if first.Transport == nil {
		t.Fatal("expected transport to be set")
	}
	if first.Transport != second.Transport {
		t.Fatal("expected shared transport instance")
	}

	transport, ok := first.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", first.Transport)
	}
	if transport.MaxIdleConns < sharedHTTPTransportMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want >= %d", transport.MaxIdleConns, sharedHTTPTransportMaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost < sharedHTTPTransportMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want >= %d", transport.MaxIdleConnsPerHost, sharedHTTPTransportMaxIdleConnsPerHost)
	}
	if !transport.ForceAttemptHTTP2 {
		t.Fatal("expected ForceAttemptHTTP2 to be enabled")
	}
}

func TestNewHTTPClientPreservesTimeout(t *testing.T) {
	client := NewHTTPClient(7 * time.Second)
	if client.Timeout != 7*time.Second {
		t.Fatalf("Timeout = %v, want %v", client.Timeout, 7*time.Second)
	}

	withoutTimeout := NewHTTPClient(0)
	if withoutTimeout.Timeout != 0 {
		t.Fatalf("Timeout = %v, want 0", withoutTimeout.Timeout)
	}
}
