package php

import (
	"bytes"
	"encoding/json"
	"io"

	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
)

// SetRequest is the canonical non-secret php.set input. This is an input
// validator only; it does not grant authority or publish any configuration.
type SetRequest struct {
	SiteID  string      `json:"site_id"`
	Version string      `json:"version"`
	Limits  LimitsPatch `json:"limits,omitempty"`
}

// DecodeSetRequest rejects duplicate keys, nulls, unknown keys and trailing
// values before accepting persisted job arguments. Merging the patch with the
// current tuple and site eligibility must happen under the site's job lock.
func DecodeSetRequest(raw []byte) (SetRequest, error) {
	invalid := rpcerr.Errf("invalid_argument", "Invalid PHP request")
	var result SetRequest
	if len(raw) > 4096 {
		return result, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := setObject(d, map[string]bool{"site_id": true, "version": true, "limits": true}, true); err != nil {
		return result, invalid
	}
	if _, err := d.Token(); err != io.EOF {
		return result, invalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&result); err != nil || !id.Valid(result.SiteID) || result.Version != "8.3" {
		return SetRequest{}, invalid
	}
	return result, nil
}

func setObject(d *json.Decoder, allowed map[string]bool, outer bool) error {
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return io.ErrUnexpectedEOF
	}
	seen := map[string]bool{}
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok || !allowed[key] || seen[key] {
			return io.ErrUnexpectedEOF
		}
		seen[key] = true
		if outer && key == "limits" {
			if err = setObject(d, map[string]bool{"memory_mb": true, "upload_mb": true, "max_exec_sec": true}, false); err != nil {
				return err
			}
		} else {
			var v json.RawMessage
			if err = d.Decode(&v); err != nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return io.ErrUnexpectedEOF
			}
		}
	}
	if _, err = d.Token(); err != nil {
		return err
	}
	if outer && (!seen["site_id"] || !seen["version"]) {
		return io.ErrUnexpectedEOF
	}
	return nil
}
