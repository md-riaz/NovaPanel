package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/mail"
	"net/url"

	"github.com/go-acme/lego/v4/registration"
	"novapanel/internal/secret"
)

type Environment string

const (
	Staging    Environment = "staging"
	Production Environment = "prod"
)

// AccountRecord contains ciphertext only and can be saved by the agent in secrets.
type AccountRecord struct {
	Name   string        `json:"name"`
	Sealed secret.Sealed `json:"sealed"`
}

func (r AccountRecord) String() string   { return "ACME account (sealed)" }
func (r AccountRecord) GoString() string { return r.String() }

type accountData struct {
	Name         string
	Directory    string
	Email        string
	Key          []byte
	Registration *registration.Resource
}
type account struct {
	data accountData
	key  crypto.PrivateKey
}

func (a *account) GetEmail() string                        { return a.data.Email }
func (a *account) GetPrivateKey() crypto.PrivateKey        { return a.key }
func (a *account) GetRegistration() *registration.Resource { return a.data.Registration }
func (a account) String() string                           { return "ACME account (redacted)" }
func (a account) GoString() string                         { return a.String() }

var errAccount = errors.New("invalid ACME account or configuration")

func accountName(env Environment) (string, error) {
	if env != Staging && env != Production {
		return "", errAccount
	}
	return "acme:account:" + string(env), nil
}
func validDirectory(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && len(raw) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && u.RawQuery == ""
}
func validEmail(email string, env Environment) bool {
	if email == "" {
		return env == Staging
	}
	a, e := mail.ParseAddress(email)
	return len(email) <= 254 && e == nil && a.Address == email
}

// NewAccount generates and seals a key without network I/O. Persist the returned
// record before use, under its exact Name. Never reuse it for another directory.
func NewAccount(s *secret.Sealer, env Environment, directory, email string) (AccountRecord, error) {
	name, e := accountName(env)
	if e != nil || s == nil || !validDirectory(directory) || !validEmail(email, env) {
		return AccountRecord{}, errAccount
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return AccountRecord{}, errAccount
	}
	der, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return AccountRecord{}, errAccount
	}
	defer clear(der)
	return sealAccount(s, accountData{Name: name, Directory: directory, Email: email, Key: der})
}
func sealAccount(s *secret.Sealer, data accountData) (AccountRecord, error) {
	plain, e := json.Marshal(data)
	if e != nil {
		return AccountRecord{}, errAccount
	}
	defer clear(plain)
	sealed, e := s.Seal(plain)
	if e != nil {
		return AccountRecord{}, errAccount
	}
	return AccountRecord{Name: data.Name, Sealed: sealed}, nil
}
func openAccount(c Config, r AccountRecord) (*account, error) {
	name, e := accountName(c.Environment)
	if e != nil || c.Sealer == nil || r.Name != name || len(r.Sealed.Ciphertext) > 64<<10 {
		return nil, errAccount
	}
	plain, e := c.Sealer.Open(r.Sealed)
	if e != nil {
		return nil, errAccount
	}
	defer clear(plain)
	var data accountData
	if json.Unmarshal(plain, &data) != nil || data.Name != name || data.Directory != c.Directory || data.Email != c.Email {
		return nil, errAccount
	}
	key, e := x509.ParsePKCS8PrivateKey(data.Key)
	if e != nil {
		return nil, errAccount
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, errAccount
	}
	if data.Registration != nil {
		u, e := url.Parse(data.Registration.URI)
		d, _ := url.Parse(c.Directory)
		if e != nil || u.Scheme != d.Scheme || u.Host != d.Host || u.User != nil || u.Fragment != "" {
			return nil, errAccount
		}
	}
	return &account{data: data, key: key}, nil
}
