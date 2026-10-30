// panel-web is an unprivileged placeholder until the HTTP phase.
package main

import (
	"fmt"
	"os"
)

var geteuid = os.Geteuid

func run() error {
	if geteuid() == 0 {
		return fmt.Errorf("panel-web refuses to run as root")
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
