// Command mater keeps Rust and Node build output in one place and reclaims it.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/fang"

	"github.com/colbylwilliams/mater/cmd"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)"
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := fang.Execute(ctx, cmd.Root(), fang.WithVersion(version)); err != nil {
		os.Exit(1)
	}
}
