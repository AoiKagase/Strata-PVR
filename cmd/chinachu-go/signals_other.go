//go:build !unix

package main

import (
	"context"
	"os"
	"os/signal"
)

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}
