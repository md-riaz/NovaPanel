package sshkey

import (
	"bytes"
	"crypto/rsa"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

type Key struct {
	ID          string `json:"id"`
	Comment     string `json:"comment"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

func Parse(s string) (ssh.PublicKey, string, error) {
	if len(s) == 0 || len(s) > 16384 || strings.ContainsAny(s, "\r\n\x00") {
		return nil, "", errors.New("invalid key")
	}
	k, comment, _, rest, err := ssh.ParseAuthorizedKey([]byte(s))
	if err != nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, "", errors.New("invalid key")
	}
	words := strings.Fields(comment)
	for i := range words {
		if _, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(strings.Join(words[i:], " "))); err == nil && len(bytes.TrimSpace(rest)) == 0 {
			return nil, "", errors.New("multiple keys")
		}
	}
	switch k.Type() {
	case ssh.KeyAlgoED25519:
	case ssh.KeyAlgoRSA:
		ck, ok := k.(ssh.CryptoPublicKey)
		if !ok {
			return nil, "", errors.New("invalid RSA key")
		}
		r, ok := ck.CryptoPublicKey().(*rsa.PublicKey)
		if !ok || r.N.BitLen() < 2048 || r.N.BitLen() > 8192 {
			return nil, "", errors.New("invalid RSA key")
		}
	default:
		return nil, "", errors.New("unsupported key")
	}
	return k, comment, nil
}
