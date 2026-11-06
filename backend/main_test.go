package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bosapi/backend/internal/gateway"
	"bosapi/backend/internal/server"
)

func TestRunStopsOnContextCancellation(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("BOS_DATA_FILE", t.TempDir()+"/gateway.enc")
	t.Setenv("BOS_DATABASE_DSN", "")
	t.Setenv("BOS_LISTEN", "127.0.0.1:0")
	t.Setenv("BOS_CALLBACK_LISTEN", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, gateway.BackgroundServices{}) }()
	t.Cleanup(cancel)
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gateway did not shut down after cancellation")
	}
}

func TestServeWebKeepsAPIRoutesAndServesFrontend(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("frontend"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "app.js"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	handler := server.ServeWeb(api, directory)
	for _, test := range []struct {
		path string
		code int
		body string
	}{
		{path: "/", code: http.StatusOK, body: "frontend"},
		{path: "/login", code: http.StatusOK, body: "frontend"},
		{path: "/app.js", code: http.StatusOK, body: "asset"},
		{path: "/api/v1/missing", code: http.StatusTeapot},
		{path: "/health", code: http.StatusTeapot},
		{path: "/missing.js", code: http.StatusTeapot},
	} {
		t.Run(test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != test.code || test.body != "" && recorder.Body.String() != test.body {
				t.Fatalf("GET %s: status %d body %q", test.path, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestServeWebCachePolicy(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"index.html", "assets/index-Ab12_cd3.js", "assets/index-Ab12_cd3.css", "assets/custom.js", "logo.svg"} {
		filename := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusTeapot)
	})
	handler := server.ServeWeb(api, directory)
	for _, test := range []struct {
		path  string
		cache string
		code  int
	}{
		{"/", "no-cache, no-store, must-revalidate", http.StatusOK},
		{"/login", "no-cache, no-store, must-revalidate", http.StatusOK},
		{"/admin/keys", "no-cache, no-store, must-revalidate", http.StatusOK},
		{"/assets/index-Ab12_cd3.js", "public, max-age=31536000, immutable", http.StatusOK},
		{"/assets/index-Ab12_cd3.css", "public, max-age=31536000, immutable", http.StatusOK},
		{"/assets/custom.js", "no-cache", http.StatusOK},
		{"/logo.svg", "no-cache", http.StatusOK},
		{"/api/v1/keys", "no-store", http.StatusTeapot},
		{"/assets/missing-Ab12_cd3.js", "no-store", http.StatusTeapot},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+test.path, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(method, test.path, nil))
				if recorder.Code != test.code || recorder.Header().Get("Cache-Control") != test.cache {
					t.Fatalf("status %d, Cache-Control %q", recorder.Code, recorder.Header().Get("Cache-Control"))
				}
			})
		}
	}

	// Revalidation still works for cached assets when the browser sends a validator.
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/assets/index-Ab12_cd3.js", nil))
	request := httptest.NewRequest(http.MethodGet, "/assets/index-Ab12_cd3.js", nil)
	request.Header.Set("If-Modified-Since", first.Header().Get("Last-Modified"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotModified || recorder.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("conditional GET: status %d, headers %v", recorder.Code, recorder.Header())
	}
}

func TestServeWebGatewayAliasBoundaries(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("frontend"), 0o600); err != nil {
		t.Fatal(err)
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-URI", r.URL.RequestURI())
		w.WriteHeader(http.StatusTeapot)
	})
	handler := server.ServeWeb(api, directory)
	for _, prefix := range []string{"/api/v1", "/v1", "/v1beta", "/health", "/images", "/models", "/responses", "/messages", "/chat/completions", "/embeddings"} {
		for _, suffix := range []string{"", "/", "/example", "/example.js", "?limit=2"} {
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
				t.Run(method+prefix+suffix, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, httptest.NewRequest(method, prefix+suffix, nil))
					if recorder.Code != http.StatusTeapot || recorder.Header().Get("X-Backend-URI") != prefix+suffix {
						t.Fatalf("alias did not retain its backend URI: status %d, headers %v", recorder.Code, recorder.Header())
					}
				})
			}
		}
		for _, suffix := range []string{"-page", "extra"} {
			t.Run(prefix+suffix, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, prefix+suffix, nil))
				if recorder.Code != http.StatusOK || recorder.Body.String() != "frontend" {
					t.Fatalf("prefix lookalike bypassed frontend: status %d", recorder.Code)
				}
			})
		}
	}
}
