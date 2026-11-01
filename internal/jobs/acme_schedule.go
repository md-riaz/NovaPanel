package jobs

import (
	"encoding/json"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
)

func automaticACMEPolicy() SchedulePolicy {
	return SchedulePolicy{Effectful: true, ObjectKey: func(raw json.RawMessage) (string, error) {
		var v struct {
			SiteID  string `json:"site_id"`
			Staging *bool  `json:"staging"`
		}
		if json.Unmarshal(raw, &v) != nil || !id.Valid(v.SiteID) || v.Staging == nil {
			return "", rpcerr.Errf("invalid_argument", "Invalid automatic certificate schedule")
		}
		return "site:" + v.SiteID, nil
	}}
}
