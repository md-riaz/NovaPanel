package php

import (
	"fmt"

	"novapanel/internal/validate"
)

// DedicatedMaster renders a single-site FPM master configuration. The caller
// must publish both files under root custody before starting its own domain.
func DedicatedMaster(name string) ([]byte, error) {
	if err := (&validate.V{}).SiteName(name); err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("[global]\ndaemonize = no\ninclude = /etc/novapanel/fpm/pools/%s.conf\n", name)), nil
}
