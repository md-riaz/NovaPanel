package fixture

import (
	"novapanel/tools/arch"
	"testing"
)

func TestArchitecture(t *testing.T) {
	for _, problem := range arch.Check(".") {
		t.Error(problem)
	}
}
