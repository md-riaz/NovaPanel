package rpc

import (
	"encoding/json"
	"strings"

	"novapanel/internal/id"
	"novapanel/internal/validate"
)

// Request attribution never reads state or asserts that the target exists.
func requestObjectKey(req Request) string {
	var target struct {
		SiteID     string `json:"site_id"`
		DomainID   string `json:"domain_id"`
		RedirectID string `json:"redirect_id"`
		Name       string `json:"name"`
	}
	if json.Unmarshal(req.Args, &target) != nil {
		return ""
	}
	switch req.Verb {
	case "site.create":
		if target.Name != "" && (&validate.V{}).SiteName(target.Name) == nil {
			return "site:" + target.Name
		}
	case "domain.remove":
		if id.Valid(target.DomainID) {
			return "domain:" + target.DomainID
		}
	case "redirect.remove":
		if id.Valid(target.RedirectID) {
			return "redirect:" + target.RedirectID
		}
	default:
		if (strings.HasPrefix(req.Verb, "site.") || strings.HasPrefix(req.Verb, "domain.") || strings.HasPrefix(req.Verb, "redirect.") || strings.HasPrefix(req.Verb, "ssl.")) && id.Valid(target.SiteID) {
			return "site:" + target.SiteID
		}
	}
	return ""
}
