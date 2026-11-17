//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "macawlink: the receiver runs on Windows. Build with GOOS=windows (see windows/build.sh).")
	os.Exit(1)
}
