//go:build !wordpress_offline_lab || !linux

package store

import "context"

func freshCheckpointTestHook(context.Context, string, string) {}
