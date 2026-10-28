package store

import "github.com/biubiubiu125/icloud-mail/internal/protocol"

func protocolRandomID() (string, error) {
	return protocol.RandomID()
}

func protocolRandomToken() (string, error) {
	return protocol.RandomToken(24)
}
