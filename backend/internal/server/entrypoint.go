// Package server runs the gateway process and shared static web delivery.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"bosapi/backend/internal/gateway"
	"bosapi/backend/internal/platform/database"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/subscription"
	"bosapi/backend/migrations"
)

// Run starts migrations, gateway dependencies, and the HTTP servers. Edition
// entry points inject their own optional security dependencies.
func Run(ctx context.Context, services gateway.BackgroundServices) error {
	cfg, err := gateway.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	stopCodexVersionSync, err := subscription.StartCodexVersionSync(ctx)
	if err != nil {
		return fmt.Errorf("configure Codex client version: %w", err)
	}
	defer stopCodexVersionSync()
	if cfg.DatabaseDSN != "" {
		if err := database.ApplyMigrations(ctx, cfg.DatabaseDSN, migrations.Files, database.InitialAdmin{
			Email: os.Getenv("BOS_ADMIN_EMAIL"), Password: os.Getenv("BOS_ADMIN_PASSWORD"),
		}); err != nil {
			return fmt.Errorf("apply SQL migrations: %w", err)
		}
	}

	g, err := gateway.NewWithBackgroundServices(cfg, services)
	if err != nil {
		return fmt.Errorf("initialize gateway: %w", err)
	}
	defer func() {
		if err := g.Close(); err != nil {
			logging.Printf(context.Background(), "ERROR", "server.shutdown.gateway_close_failed", "err=%v", err)
		}
	}()
	mainListener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}
	defer mainListener.Close()
	callbackListener, err := net.Listen("tcp", cfg.CallbackListen)
	if err != nil {
		return fmt.Errorf("listen on OAuth callback %s: %w", cfg.CallbackListen, err)
	}
	defer callbackListener.Close()

	handler := g.Handler()
	if webDir := os.Getenv("BOS_WEB_DIR"); webDir != "" {
		if _, err := os.Stat(filepath.Join(webDir, "index.html")); err != nil {
			return fmt.Errorf("load web assets: %w", err)
		}
		handler = ServeWeb(handler, webDir)
	}
	server := &http.Server{Handler: handler, WriteTimeout: cfg.GatewayTimeout, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	callback := &http.Server{Handler: g.CallbackHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 2)
	go func() { errs <- server.Serve(mainListener) }()
	go func() { errs <- callback.Serve(callbackListener) }()
	logging.Printf(context.Background(), "INFO", "server.started", "gateway_address=%s oauth_callback_address=%s", mainListener.Addr(), callbackListener.Addr())

	signalCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var serveErr error
	select {
	case <-signalCtx.Done():
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		serveErr = errors.Join(serveErr, fmt.Errorf("shutdown HTTP server: %w", err))
	}
	if err := callback.Shutdown(shutdownCtx); err != nil {
		_ = callback.Close()
		serveErr = errors.Join(serveErr, fmt.Errorf("shutdown HTTP server: %w", err))
	}
	return serveErr
}

// versionedWebAsset recognizes Vite's hashed asset names.
var versionedWebAsset = regexp.MustCompile(`^assets/.+-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

// ServeWeb serves the built frontend while preserving API routes.
func ServeWeb(api http.Handler, directory string) http.Handler {
	files := os.DirFS(directory)
	static := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := r.URL.Path
		if r.Method != http.MethodGet && r.Method != http.MethodHead || isAPIPath(requestPath) {
			api.ServeHTTP(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(requestPath), "/")
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			http.ServeFile(w, r, filepath.Join(directory, "index.html"))
			return
		}
		if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
			if versionedWebAsset.MatchString(name) {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				// Public files without a content hash must be revalidated after deployment.
				w.Header().Set("Cache-Control", "no-cache")
			}
			static.ServeHTTP(w, r)
			return
		}
		if name == "" || name == "." || !strings.Contains(path.Base(name), ".") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			http.ServeFile(w, r, filepath.Join(directory, "index.html"))
			return
		}
		api.ServeHTTP(w, r)
	})
}

func isAPIPath(requestPath string) bool {
	for _, prefix := range [...]string{"/api/v1", "/v1", "/v1beta", "/images", "/health", "/models", "/responses", "/messages", "/chat/completions", "/embeddings"} {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}
