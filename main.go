package main

import (
	"io/fs"
	"log"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/server"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
)

func main() {
	cfgStore, err := config.NewStore()
	if err != nil {
		log.Fatalf("Failed to initialize config store: %v", err)
	}

	workerMgr := worker.NewManager(workerScript)
	dialogs := newDesktopDialogs()

	srv := server.New(cfgStore, workerMgr, dialogs)
	subDemo, err := fs.Sub(demoProjectFS, "examples/demo_project")
	if err != nil {
		log.Fatalf("Failed to mount embedded demo project: %v", err)
	}
	srv.SetScaffoldAssets(subDemo, canonicalParamsPy)

	app := newApplication(srv, dialogs)

	// Start persistent Python worker & fsnotify watcher in background
	go srv.StartServices()
	defer srv.Shutdown()

	attachDesktopWindow(app, srv)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
