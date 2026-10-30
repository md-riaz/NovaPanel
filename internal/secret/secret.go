// Package secret seals agent-only data and redacts credentials before logging.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
)

type Sealed struct {
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}
type Sealer struct{ aead cipher.AEAD }

func New(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("seal key must be 32 bytes")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	return &Sealer{a}, nil
}
func (s *Sealer) Seal(plain []byte) (Sealed, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		return Sealed{}, e
	}
	return Sealed{nonce, s.aead.Seal(nil, nonce, plain, nil)}, nil
}
func (s *Sealer) Open(v Sealed) ([]byte, error) {
	if len(v.Nonce) != s.aead.NonceSize() {
		return nil, errors.New("invalid seal nonce")
	}
	return s.aead.Open(nil, v.Nonce, v.Ciphertext, nil)
}

// LoadOrCreate publishes a complete, synced key atomically. Existing keys must
// belong to this process uid (root in production) and have exactly 0600 mode.
func LoadOrCreate(path string) (*Sealer, error) {
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		key := make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		defer clear(key)
		f, e := os.CreateTemp(filepath.Dir(path), ".seal-key-*")
		if e != nil {
			return nil, e
		}
		tmp := f.Name()
		defer os.Remove(tmp)
		if _, e = f.Write(key); e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
		if e = os.Link(tmp, path); e != nil && !os.IsExist(e) {
			return nil, e
		}
		if e = os.Remove(tmp); e != nil {
			return nil, e
		}
		dir, e := os.Open(filepath.Dir(path))
		if e != nil {
			return nil, e
		}
		e = dir.Sync()
		dir.Close()
		if e != nil {
			return nil, e
		}
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return nil, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Size != 32 || st.Nlink != 1 {
		return nil, errors.New("unsafe seal key file")
	}
	key, e := io.ReadAll(io.LimitReader(f, 33))
	if e != nil {
		return nil, e
	}
	defer clear(key)
	return New(key)
}
