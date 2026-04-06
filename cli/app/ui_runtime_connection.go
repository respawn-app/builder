package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"

	"builder/server/llm"
	"builder/shared/serverapi"
)

const runtimeDisconnectedStatusMessage = "server disconnected"

func (m *uiModel) observeRuntimeRequestResult(err error) {
	if m == nil || !m.hasRuntimeClient() {
		return
	}
	if err == nil {
		m.setRuntimeDisconnected(false)
		return
	}
	if isRuntimeConnectionError(err) {
		m.setRuntimeDisconnected(true)
		return
	}
	if confirmsRuntimeReachability(err) {
		m.setRuntimeDisconnected(false)
	}
}

func (m *uiModel) runtimeDisconnectStatusVisible() bool {
	return m != nil && m.hasRuntimeClient() && m.runtimeDisconnectedState()
}

func (m *uiModel) runtimeDisconnectStatusText() string {
	if !m.runtimeDisconnectStatusVisible() {
		return ""
	}
	return runtimeDisconnectedStatusMessage
}

func (m *uiModel) setRuntimeDisconnected(disconnected bool) {
	if m == nil {
		return
	}
	m.runtimeDisconnected = disconnected
}

func (m *uiModel) runtimeDisconnectedState() bool {
	if m == nil {
		return false
	}
	return m.runtimeDisconnected
}

func isRuntimeConnectionError(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *llm.APIStatusError
	if errors.As(err, &statusErr) {
		return false
	}
	if errors.Is(err, serverapi.ErrStreamGap) || errors.Is(err, serverapi.ErrStreamUnavailable) || errors.Is(err, serverapi.ErrStreamFailed) {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if errors.Is(urlErr.Err, context.DeadlineExceeded) || errors.Is(urlErr.Err, context.Canceled) {
			return false
		}
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, context.DeadlineExceeded) || errors.Is(opErr.Err, context.Canceled) {
			return false
		}
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && !netErr.Timeout()
}

func confirmsRuntimeReachability(err error) bool {
	if err == nil {
		return true
	}
	if isRuntimeConnectionError(err) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, serverapi.ErrStreamGap) || errors.Is(err, serverapi.ErrStreamUnavailable) || errors.Is(err, serverapi.ErrStreamFailed) {
		return false
	}
	return true
}
