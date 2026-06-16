//go:build darwin

package main

import (
	"context"
	"errors"
	"log"
	"net/http"
)

// runMainLoop flips the loop on macOS: Cocoa requires the event loop on the OS
// main thread, so the tray blocks the main goroutine while the HTTP server runs
// in a background goroutine. Whichever stops first cancels the other.
func runMainLoop(trayRun func(context.Context) error, trayCtx context.Context, serverServe func() error) error {
	innerCtx, cancel := context.WithCancel(trayCtx)
	defer cancel()

	serverErr := make(chan error, 1)
	go func() { serverErr <- serverServe() }()

	var firstServerErr error
	go func() {
		if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			firstServerErr = err
			log.Printf("launcher: console server stopped: %v", err)
		}
		cancel()
	}()

	// trayRun blocks the OS main thread (Cocoa/systray requirement).
	if err := trayRun(innerCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("launcher: tray run: %v", err)
	}
	return firstServerErr
}
