package auth

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestPasswordPHC(t *testing.T) {
	phc := Hash("a sufficiently long password")
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatal(phc)
	}
	if ok, rehash := Verify("a sufficiently long password", phc); !ok || rehash {
		t.Fatal(ok, rehash)
	}
	if ok, _ := Verify("wrong", phc); ok {
		t.Fatal("wrong password")
	}
	salt := bytes.Repeat([]byte{42}, 16)
	// Expected hash independently computed with libargon2 argon2id_hash_raw.
	weak := fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$%s$yj601DUth0a4EptCqIumdHhvbbS9VSmIznW3AoXb2n4", base64.RawStdEncoding.EncodeToString(salt))
	if ok, rehash := Verify("known", weak); !ok || !rehash {
		t.Fatal(ok, rehash)
	}
}
