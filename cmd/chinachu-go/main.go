package main

import (
	"fmt"
	"os"

	"chinachu-go/internal/cli"
)

func main() {
	ctx, stop := signalContext()
	defer stop()
	if err := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
