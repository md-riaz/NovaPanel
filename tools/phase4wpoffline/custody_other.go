//go:build wordpress_offline_lab && !linux

package main

import "os"

func lockLab(string) (*os.File, error) { return nil, errRuntimeRefused }
func labConfig(string) error           { return errRuntimeRefused }
func labArtifacts(string) error        { return errRuntimeRefused }
