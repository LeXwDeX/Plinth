package chat

import (
	"crypto/rand"
	"encoding/base64"
)

func randomID(kind string) string {
	var value [18]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return kind + "_" + base64.RawURLEncoding.EncodeToString(value[:])
}
