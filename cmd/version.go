package cmd

import (
	"fmt"
	"io"
)

var version = "dev"

func runVersion(stdout io.Writer) int {
	fmt.Fprintf(stdout, "codex-proxy %s\n", version)
	return 0
}
