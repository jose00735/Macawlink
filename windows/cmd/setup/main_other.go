//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "MacawlinkSetup runs on Windows; build it with windows/build.sh")
	os.Exit(1)
}
