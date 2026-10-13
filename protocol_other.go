//go:build !windows

package main

import "fmt"

const magnetProtocolSupported = false

func registerMagnetProtocol(string) error {
	return fmt.Errorf("automatic magnet registration is supported on Windows")
}

func unregisterMagnetProtocol(string) error {
	return fmt.Errorf("magnet unregistration is supported on Windows")
}
