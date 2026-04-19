//go:build !unix

package serve

import (
	"net"

	"builder/shared/config"
)

func listenLocalSocket(config.App) (net.Listener, func(), bool, error) {
	return nil, nil, false, nil
}
