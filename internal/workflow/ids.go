package workflow

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func newID(prefix string) string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return prefix + "_" + time.Now().UTC().Format("20060102T150405") + "_" + hex.EncodeToString(buf)
}
