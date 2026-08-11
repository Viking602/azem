//go:build !darwin || !cgo

package netproxy

func loadSystemProxy() (Settings, error) {
	return Settings{}, nil
}
