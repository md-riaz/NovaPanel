package verbs

import (
	"context"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/jobs"
)

func TestFreshWordPressJobBinding(t *testing.T) {
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if err := (&siteCreateJobArgs{SiteID: id}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (&siteCreateJobArgs{SiteID: "invalid"}).Validate(); err == nil {
		t.Fatal("invalid ID accepted")
	}
	if err := freshWordPressSiteCreateExec(context.Background(), &agent.Agent{}, &jobs.Job{ID: id, ObjectKey: "site:other"}, id); err == nil {
		t.Fatal("unbound job accepted")
	}
}
