package auth

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"golang.org/x/crypto/argon2"
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
	weak := fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(argon2.IDKey([]byte("known"), salt, 1, 8192, 1, 32)))
	if ok, rehash := Verify("known", weak); !ok || !rehash {
		t.Fatal(ok, rehash)
	}
}
