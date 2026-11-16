//go:build wordpress_offline_lab && !linux

package main

func labConfig(string) error { return errRuntimeRefused }
