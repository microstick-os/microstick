package main

import (
	"fmt"
	"os"

	"microstick.dev/tools/internal/dev"
)

func main() {
	if err := dev.Main(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "microstick:", err)
		os.Exit(1)
	}
}
