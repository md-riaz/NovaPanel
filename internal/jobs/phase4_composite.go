package jobs

import (
	"errors"

	"novapanel/internal/store"
)

func compositeChildKinds(php, domainTLS bool) []string {
	var kinds []string
	if php && domainTLS {
		kinds = append(kinds, "maintenance_on")
	}
	if php {
		kinds = append(kinds, "php_change")
	}
	if domainTLS {
		kinds = append(kinds, "domain_tls")
	}
	return append(kinds, "maintenance_off")
}

// nextCompositeChild selects only pending work; a failure routes to the finalizer.
func nextCompositeChild(children []store.OperationChild) (int, error) {
	failed, finalizer := false, -1
	for i, child := range children {
		if child.Ordinal != i || (child.Status != "pending" && child.Status != "committed" && child.Status != "failed") {
			return -1, errors.New("invalid composite children")
		}
		if child.Status == "failed" {
			failed = true
		}
		if child.Kind == "maintenance_off" {
			if finalizer != -1 || i != len(children)-1 {
				return -1, errors.New("invalid composite finalizer")
			}
			finalizer = i
		}
	}
	if finalizer < 0 {
		return -1, errors.New("missing composite finalizer")
	}
	if failed {
		if children[finalizer].Status == "pending" {
			return finalizer, nil
		}
		return -1, nil
	}
	for i, child := range children {
		if child.Status == "pending" {
			return i, nil
		}
	}
	return -1, nil
}
