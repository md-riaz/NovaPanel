package rpc

import (
	"errors"
	"novapanel/internal/jobs"
	"testing"
)

func TestRecoveryBlockedErrorReply(t *testing.T) {
	r := errorReply(errors.Join(jobs.ErrRecoveryBlocked, errors.New("private recovery detail")))
	if r.OK || r.Code != "precondition_failed" || r.Error != "Startup recovery unresolved" {
		t.Fatalf("unexpected reply: %+v", r)
	}
}
