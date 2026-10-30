package rpcerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodedError(t *testing.T) {
	e := Errf("invalid_argument", "bad %s", "name")
	var got *CodedError
	if !errors.As(fmt.Errorf("wrapped: %w", e), &got) || got.Code != "invalid_argument" || got.Error() != "bad name" {
		t.Fatal(e)
	}
}
