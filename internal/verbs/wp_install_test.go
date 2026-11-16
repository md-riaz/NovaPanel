package verbs

import (
	"context"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/rpc"
)

func TestWPInstallArguments(t *testing.T) {
	v := wpInstallArgs{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Title: "Example", AdminUsername: "admin", AdminEmail: "admin@example.test"}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	v.AdminEmail = "invalid"
	if err := v.Validate(); err == nil {
		t.Fatal("invalid administrator accepted")
	}
	v.AdminEmail, v.SiteID = "admin@example.test", "invalid"
	if err := v.Validate(); err == nil {
		t.Fatal("invalid site accepted")
	}
}

func TestDeferredWordPressVerbs(t *testing.T) {
	for _, name := range []string{"wp.update", "wp.clone", "wp.staging"} {
		v, ok := rpc.Registry[name]
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		_, err := v.Run(context.Background(), &agent.Agent{}, v.NewArgs())
		coded, ok := err.(*rpc.CodedError)
		if !ok || coded.Code != "unsupported" {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
