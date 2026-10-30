package version

import "testing"

func TestBuildMetadata(t *testing.T) {
	if Version == "" || Get().Version != Version || Get().GoVersion == "" {
		t.Fatalf("%+v", Get())
	}
}
