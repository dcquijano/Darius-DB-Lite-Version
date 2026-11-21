//go:build windows && !embedzip

package main

func embeddedPayloadBytes() []byte {
	return nil
}
