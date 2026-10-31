package wordpress

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestOfficialChecksumFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/wordpress-7.1.4-checksums.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(b)
	if hex.EncodeToString(digest[:]) != ApprovedArtifacts().ChecksumsSHA256 {
		t.Fatal("official metadata bytes changed")
	}
	var metadata struct {
		Checksums map[string]string `json:"checksums"`
	}
	if err := json.Unmarshal(b, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata.Checksums) != ApprovedArtifacts().ChecksumEntries {
		t.Fatal("incomplete official metadata fixture")
	}
}

func TestSecretFreeContract(t *testing.T) {
	// Secrets cannot enter this API. A future adapter must not add a password,
	// stdin reader, arbitrary environment map, or generic extra-argument field.
	for _, typ := range []reflect.Type{reflect.TypeFor[InstallInput](), reflect.TypeFor[Administrator](), reflect.TypeFor[ArtifactEvidence](), reflect.TypeFor[Plan](), reflect.TypeFor[Step]()} {
		for n := 0; n < typ.NumField(); n++ {
			f := typ.Field(n)
			if strings.Contains(strings.ToLower(f.Name), "password") || f.Type.Kind() == reflect.Interface || f.Type.Kind() == reflect.Map {
				t.Fatalf("secret-bearing field: %s.%s", typ.Name(), f.Name)
			}
		}
	}
	p, err := BuildInstall(validInput(), artifactEvidence(t))
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"--dbpass=", "--admin_password=", "--admin_pass=", "--allow-root", "--insecure", "--extra-php", "--force", "--require=", "--exec="} {
		if bytes.Contains(serialized, []byte(forbidden)) {
			t.Fatalf("forbidden option %s", forbidden)
		}
	}
	secret := "CANARY_NEVER_LOG_password"
	for _, bad := range []func(*InstallInput){func(i *InstallInput) { i.SiteName = secret }, func(i *InstallInput) { i.PrimaryDomain = secret + "/" }, func(i *InstallInput) { i.Administrator.Email = secret + "\n" }, func(i *InstallInput) { i.Administrator.Username = secret + "\n" }} {
		in := validInput()
		bad(&in)
		rejected, err := BuildInstall(in, artifactEvidence(t))
		if err == nil {
			t.Fatal("malformed secret-shaped input accepted")
		}
		encoded, _ := json.Marshal(struct {
			Plan  Plan
			Error string
		}{rejected, err.Error()})
		for _, s := range []string{string(encoded), fmt.Sprintf("%v %+v %#v", rejected, rejected, rejected), fmt.Sprintf("%v %+v %#v", err, err, err)} {
			if strings.Contains(s, secret) {
				t.Fatal("refusal leaked input")
			}
		}
	}
	// Unknown secret fields cannot be represented by this nonsecret envelope.
	var in InstallInput
	decoder := json.NewDecoder(strings.NewReader(`{"admin_password":"` + secret + `"}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err == nil {
		t.Fatal("strict envelope accepted password")
	}
	b, _ := json.Marshal(in)
	if strings.Contains(string(b), secret) || strings.Contains(fmt.Sprintf("%#v", in), secret) {
		t.Fatal("input retained unknown password")
	}
	if p.Steps[2].Stdin == p.Steps[4].Stdin || p.Steps[2].Stdin != DatabasePassword || p.Steps[4].Stdin != AdministratorPassword {
		t.Fatal("shared secret channel")
	}
}
