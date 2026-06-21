package cmd

import (
	"fmt"
	"io"
)

var version = "2.0.0-alpha.23"

func runVersion(stdout io.Writer) int {
	fmt.Fprintf(stdout, "codex-proxy %s\n", version)
	return 0
}
