//go:build !unix

package config

func ServerLocalRPCSocketPath(App) (string, bool, error) {
	return "", false, nil
}
