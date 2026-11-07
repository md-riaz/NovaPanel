package verbs

import "testing"

func TestMarkedAccountsOnly(t *testing.T) {
	const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	input := "demo:x:100:100:novapanel-site=" + id + ":/srv/demo:/usr/sbin/nologin\n" +
		"foreign:x:101:101::/srv/foreign:/usr/sbin/nologin\n" +
		"kept:x:102:102:novapanel-retained=" + id + ":/srv/kept:/usr/sbin/nologin\n"
	got, err := markedAccounts([]byte(input))
	if err != nil || len(got) != 1 || got[0].name != "demo" || got[0].id != id {
		t.Fatalf("marked accounts: %#v, %v", got, err)
	}
}
