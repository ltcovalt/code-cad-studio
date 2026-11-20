package main

import (
	"embed"
	"log"

	"github.com/ltcovalt/code-cad-studio/internal/config"
	"github.com/ltcovalt/code-cad-studio/internal/server"
	"github.com/ltcovalt/code-cad-studio/internal/worker"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed worker/worker.py
var workerScript []byte

// wailsDialogs bridges Wails3 native OS dialogs into internal/server.DesktopDialogs.
type wailsDialogs struct {
	app *application.App
}

func (d *wailsDialogs) ChooseDirectory(title, startDir string) (string, error) {
	dlg := d.app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseDirectories(true).
		CanChooseFiles(false)
	if startDir != "" {
		dlg.SetDirectory(startDir)
	}
	return dlg.PromptForSingleSelection()
}

func (d *wailsDialogs) ChooseFile(title, startDir string) (string, error) {
	dlg := d.app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseFiles(true).
		CanChooseDirectories(false)
	if startDir != "" {
		dlg.SetDirectory(startDir)
	}
	return dlg.PromptForSingleSelection()
}

func (d *wailsDialogs) SaveFile(title, defaultFilename, filterName, filterPattern string) (string, error) {
	dlg := d.app.Dialog.SaveFile().
		SetFilename(defaultFilename).
		AddFilter(filterName, filterPattern)
	if title != "" {
		dlg.SetMessage(title)
	}
	return dlg.PromptForSingleSelection()
}

// wailsWindow bridges Wails3 frameless window controls into internal/server.DesktopWindow.
type wailsWindow struct {
	win *application.WebviewWindow
}

func (w *wailsWindow) Minimise() {
	if w.win != nil {
		w.win.Minimise()
	}
}

func (w *wailsWindow) ToggleMaximise() bool {
	if w.win == nil {
		return false
	}
	w.win.ToggleMaximise()
	return w.win.IsMaximised()
}

func (w *wailsWindow) IsMaximised() bool {
	if w.win == nil {
		return false
	}
	return w.win.IsMaximised()
}

func (w *wailsWindow) Close() {
	if w.win != nil {
		w.win.Close()
	}
}

func main() {
	cfgStore, err := config.NewStore()
	if err != nil {
		log.Fatalf("Failed to initialize config store: %v", err)
	}

	workerMgr := worker.NewManager(workerScript)
	dialogs := &wailsDialogs{}

	srv := server.New(cfgStore, workerMgr, dialogs)

	app := application.New(application.Options{
		Name:        "Code CAD Studio",
		Description: "Model-agnostic parametric GUI for build123d",
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(assets),
			Middleware: srv.Middleware,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	dialogs.app = app

	// Start persistent Python worker & fsnotify watcher in background
	go srv.StartServices()
	defer srv.Shutdown()

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Code CAD Studio",
		Width:            1400,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        680,
		Frameless:        true,
		BackgroundColour: application.NewRGB(25, 30, 36),
		URL:              "/",
		Windows: application.WindowsWindow{
			NonClientRegionSupport: true,
		},
	})
	srv.SetWindow(&wailsWindow{win: win})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
