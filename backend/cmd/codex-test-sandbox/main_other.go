//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "Codex filesystem sandbox requires Linux with Landlock ABI 2 or newer")
	os.Exit(1)
}
