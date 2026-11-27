package agent

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"novapanel/internal/fileop"
	"novapanel/internal/id"
)

// SweepUploads protects live job inputs while removing abandoned uploads.
func (a *Agent) SweepUploads(ctx context.Context, now time.Time) error {
	a.UploadsMu.Lock()
	defer a.UploadsMu.Unlock()
	args, err := a.Store.LiveJobArgs(ctx, "files.upload")
	if err != nil {
		return err
	}
	live := make(map[string]struct{}, len(args))
	for _, arg := range args {
		var v struct {
			UploadID string `json:"upload_id"`
		}
		if json.Unmarshal(arg, &v) != nil || !id.Valid(v.UploadID) {
			log.Print("skipping malformed live upload job")
			continue
		}
		live[v.UploadID] = struct{}{}
	}
	fileop.SweepUploads(a.Cfg.UploadsDir, now, func(name string) bool { _, ok := live[name]; return ok })
	return nil
}
