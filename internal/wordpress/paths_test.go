package wordpress

import "testing"

func TestConfiguredRootComponents(t *testing.T) {
	in := validInput()
	in.SitesRoot = "/srv/panel-sites.v1"
	p, err := BuildInstall(in, artifactEvidence(t))
	if err != nil {
		t.Fatal(err)
	}
	if p.Docroot != "/srv/panel-sites.v1/example/public" {
		t.Fatal("configured root changed")
	}
	for _, root := range []string{"/srv/.hidden", "/srv/-option", "/srv/a/./sites", "/srv/a/../sites", "/srv/sites=bad"} {
		in.SitesRoot = root
		if _, err := BuildInstall(in, artifactEvidence(t)); err == nil {
			t.Fatal("accepted hostile root component")
		}
	}
}
