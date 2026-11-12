package dev

import (
	"crypto/rand"
	"encoding/hex"
)

type FormError struct {
	Field   int
	Message string
}

func (e *FormError) Error() string { return e.Message }
func NewInstallationUUID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return hex.EncodeToString(b[:]), nil
}
func DefaultSSID(id string) string {
	if len(id) < 8 {
		return ""
	}
	return "microstick-" + id[:8]
}
