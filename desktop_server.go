//go:build server

package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ltcovalt/code-cad-studio/internal/server"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func newDesktopDialogs() server.DesktopDialogs {
	return nil
}

func resolveServerPort() int {
	for _, key := range []string{"WAILS_SERVER_PORT", "PORT"} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			if p, err := strconv.Atoi(raw); err == nil && p > 0 && p <= 65535 {
				return p
			}
		}
	}
	return 8080
}

func newApplication(srv *server.Server, _ server.DesktopDialogs) *application.App {
	host := strings.TrimSpace(os.Getenv("WAILS_SERVER_HOST"))
	if host == "" {
		host = "localhost"
	}
	port := resolveServerPort()

	log.Printf("Starting Code CAD Studio server mode on http://%s:%d", host, port)

	return application.New(application.Options{
		Name:        "Code CAD Studio",
		Description: "Model-agnostic parametric GUI for build123d",
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(assets),
			Middleware: srv.ServerMiddleware,
		},
		Server: application.ServerOptions{
			Host:         host,
			Port:         port,
			WriteTimeout: 5 * time.Minute,
		},
	})
}

func attachDesktopWindow(_ *application.App, _ *server.Server) {}
