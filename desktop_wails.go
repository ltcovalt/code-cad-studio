//go:build !server

package main

import (
	"runtime"

	"github.com/ltcovalt/code-cad-studio/internal/server"
	"github.com/wailsapp/wails/v3/pkg/application"
)

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

func (d *wailsDialogs) SaveFile(title, defaultDir, defaultFilename, filterName, filterPattern string) (string, error) {
	dlg := d.app.Dialog.SaveFile().
		SetFilename(defaultFilename).
		AddFilter(filterName, filterPattern)
	if defaultDir != "" {
		dlg.SetDirectory(defaultDir)
	}
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

func newDesktopDialogs() server.DesktopDialogs {
	return &wailsDialogs{}
}

func newApplication(srv *server.Server, dialogs server.DesktopDialogs) *application.App {
	app := application.New(application.Options{
		Name:        "Code CAD Studio",
		Description: "Model-agnostic parametric GUI for build123d",
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(assets),
			Middleware: srv.DesktopMiddleware,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	if wd, ok := dialogs.(*wailsDialogs); ok {
		wd.app = app
	}
	return app
}

func attachDesktopWindow(app *application.App, srv *server.Server) {
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Code CAD Studio",
		Width:            1400,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        680,
		Frameless:        runtime.GOOS != "darwin",
		BackgroundColour: application.NewRGB(25, 30, 36),
		URL:              "/",
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 40,
		},
		Windows: application.WindowsWindow{
			NonClientRegionSupport: true,
		},
	})
	srv.SetWindow(&wailsWindow{win: win})
}
