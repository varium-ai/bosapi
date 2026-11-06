// Package main is the open-source edition entry point. It intentionally has no
// proprietary detector imports and relies on the public no-op detector.
package main

import (
	"context"
	"os"

	"bosapi/backend/internal/gateway"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/server"
)

func main() {
	if err := server.Run(context.Background(), gateway.BackgroundServices{}); err != nil {
		logging.Event(context.Background(), "ERROR", "server.startup.failed", err.Error())
		os.Exit(1)
	}
}
