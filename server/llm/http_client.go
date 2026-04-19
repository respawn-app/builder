package llm

import (
	"net/http"
	"time"
)

const (
	sharedHTTPTransportMaxIdleConns        = 128
	sharedHTTPTransportMaxIdleConnsPerHost = 32
)

var sharedHTTPTransport = newSharedHTTPTransport()

// NewHTTPClient returns an HTTP client that shares a tuned transport across
// runtimes so local/LAN model backends can reuse warm connections aggressively.
func NewHTTPClient(timeout time.Duration) *http.Client {
	client := &http.Client{Transport: sharedHTTPTransport}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}

func newSharedHTTPTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{ForceAttemptHTTP2: true}
	}
	transport := base.Clone()
	transport.ForceAttemptHTTP2 = true
	if transport.MaxIdleConns < sharedHTTPTransportMaxIdleConns {
		transport.MaxIdleConns = sharedHTTPTransportMaxIdleConns
	}
	if transport.MaxIdleConnsPerHost < sharedHTTPTransportMaxIdleConnsPerHost {
		transport.MaxIdleConnsPerHost = sharedHTTPTransportMaxIdleConnsPerHost
	}
	return transport
}
