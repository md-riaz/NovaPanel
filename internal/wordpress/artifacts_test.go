package wordpress

import (
	"encoding/json"
	"os"
	"testing"
)

func TestApprovedArtifacts(t *testing.T) {
	b, err := os.ReadFile("testdata/verified-artifacts.json")
	if err != nil {
		t.Fatal(err)
	}
	var evidence ArtifactEvidence
	if err := json.Unmarshal(b, &evidence); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArtifacts(evidence); err != nil {
		t.Fatal(err)
	}
	m := ApprovedArtifacts()
	if m.CLI.Version != "2.12.0" || m.WordPress.Version != "7.1.4" || m.Locale != "en_US" || m.ChecksumEntries != 3782 {
		t.Fatalf("unexpected approved manifest: %+v", m)
	}
	for _, mutate := range []func(*ArtifactEvidence){
		func(e *ArtifactEvidence) { e.CLISHA256 = "bad" },
		func(e *ArtifactEvidence) { e.WordPressSHA256 = "bad" },
		func(e *ArtifactEvidence) { e.ChecksumsSHA256 = "bad" },
		func(e *ArtifactEvidence) { e.SignatureVerified = false },
		func(e *ArtifactEvidence) { e.SignerFingerprint = "bad" },
		func(e *ArtifactEvidence) { e.SignerFingerprint = "63af7aa15067c05616fddd88a3a2e8f226f0bc06" },
	} {
		changed := evidence
		mutate(&changed)
		if err := ValidateArtifacts(changed); err == nil {
			t.Fatal("accepted bad artifact evidence")
		}
	}
}
