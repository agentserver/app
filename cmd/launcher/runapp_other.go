//go:build !darwin

package main

import (
	"context"
	"errors"
	"log"
)

// runMainLoop keeps the existing Windows/Linux behavior: the HTTP server owns the
// main goroutine while the tray runs in a background goroutine.
func runMainLoop(trayRun func(context.Context) error, trayCtx context.Context, serverServe func() error) error {
	go func() {
		if err := trayRun(trayCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("launcher: tray run: %v", err)
		}
	}()
	return serverServe()
}
