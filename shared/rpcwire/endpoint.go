package rpcwire

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type Transport string

const (
	TransportTCP  Transport = "tcp"
	TransportUnix Transport = "unix"
)

type Endpoint struct {
	Transport Transport
	Address   string
	ServerURL string
	OriginURL string
}

func ParseWebSocketEndpoint(raw string) (Endpoint, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Endpoint{}, errors.New("rpc endpoint is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return Endpoint{}, fmt.Errorf("parse rpc endpoint: %w", err)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return Endpoint{}, fmt.Errorf("unsupported websocket scheme %q", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return Endpoint{}, errors.New("websocket host is required")
	}
	return Endpoint{
		Transport: TransportTCP,
		Address:   parsed.Host,
		ServerURL: trimmed,
		OriginURL: websocketOrigin(parsed),
	}, nil
}

func NewUnixEndpoint(socketPath string, rpcPath string) (Endpoint, error) {
	trimmedSocketPath := strings.TrimSpace(socketPath)
	if trimmedSocketPath == "" {
		return Endpoint{}, errors.New("unix socket path is required")
	}
	trimmedPath := strings.TrimSpace(rpcPath)
	if trimmedPath == "" {
		trimmedPath = "/"
	}
	if !strings.HasPrefix(trimmedPath, "/") {
		trimmedPath = "/" + trimmedPath
	}
	serverURL := (&url.URL{Scheme: "ws", Host: "builder.local", Path: trimmedPath}).String()
	return Endpoint{
		Transport: TransportUnix,
		Address:   trimmedSocketPath,
		ServerURL: serverURL,
		OriginURL: "http://builder.local",
	}, nil
}

func websocketOrigin(parsed *url.URL) string {
	if parsed == nil {
		return "http://127.0.0.1"
	}
	scheme := "http"
	if parsed.Scheme == "wss" {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: parsed.Host}).String()
}
