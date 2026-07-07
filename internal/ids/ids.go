// Package ids creates compact random identifiers for custody resources.
package ids

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a random identifier with the provided prefix.
func New(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}
