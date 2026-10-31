// panel-update-helper currently exposes inspection only, never apply/recover.
// A future independent systemd service may pass three sealed memfds as 3/4/5.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"novapanel/internal/update"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

func run(args []string, out io.Writer) int {
	if len(args) != 1 || args[0] != "prepare" {
		fmt.Fprintln(out, "unsupported: helper apply and recovery activation remain gated")
		return 2
	}
	// Refuse an unset anchor before taking ownership of inherited descriptors.
	if update.Enabled() != nil {
		fmt.Fprintln(out, "unsupported: helper trust anchor unavailable")
		return 1
	}
	// No flags, URLs, keys, environment overrides, or caller-selected paths.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	manifest := os.NewFile(3, "manifest")
	signature := os.NewFile(4, "signature")
	bundle := os.NewFile(5, "bundle")
	if manifest != nil {
		defer manifest.Close()
	}
	if signature != nil {
		defer signature.Close()
	}
	if bundle != nil {
		defer bundle.Close()
	}
	inspection, err := update.PrepareHelper(ctx, update.HelperInstallRoot, manifest, signature, bundle)
	if err != nil {
		// Do not print paths, manifest contents, URLs or arbitrary underlying errors.
		fmt.Fprintln(out, "unsupported: helper preparation unavailable or refused")
		return 1
	}
	if json.NewEncoder(out).Encode(inspection) != nil {
		return 1
	}
	return 0
}
