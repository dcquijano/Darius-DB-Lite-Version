package main

import (
	"embed"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"dariusdb/internal/core"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	root := core.ResolveRoot()
	if len(os.Args) > 1 && os.Args[1] == "--cli" {
		os.Exit(core.RunCLI(root, os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "--httpd" {
		os.Exit(core.RunBuiltInHTTPD(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "--mailpit" {
		os.Exit(core.RunBuiltInMailpit(os.Args[2:]))
	}

	var forceShow, justUpdated, allOff, reqMinimized bool
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--minimized", "-m":
			reqMinimized = true
		case "--show":
			forceShow = true
		case "--updated":
			justUpdated = true
			forceShow = true
			allOff = true
		case "--all-off":
			allOff = true
		}
	}

	var mutexWait time.Duration
	if justUpdated || forceShow {
		mutexWait = 8 * time.Second
	}
	mutex, ok := core.AcquireSingleInstanceWithWait(root, mutexWait)
	if !ok {
		return
	}
	defer core.ReleaseSingleInstance(mutex)

	paths := core.NewPaths(root)
	if err := paths.Ensure(); err != nil {
		log.Fatalf("failed to initialize folders: %v", err)
	}
	st := core.LoadSettings(root)
	if allOff && st.Get().AutoStartServices {
		_ = st.Update(func(s *core.Settings) { s.AutoStartServices = false })
	}

	startHidden := !forceShow && (st.Get().StartMinimized || reqMinimized)

	app := NewAppWithOptions(paths, st, allOff)
	app.forceShow = forceShow
	app.justUpdated = justUpdated

	err := wails.Run(&options.App{
		Title:            "Darius DB (Lite Version 0.1.4)",
		Width:            960,
		Height:           660,
		MinWidth:         760,
		MinHeight:        520,
		StartHidden:      startHidden,
		BackgroundColour: &options.RGBA{R: 15, G: 23, B: 42, A: 255},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:     app.startup,
		OnDomReady:    app.domReady,
		OnBeforeClose: app.beforeClose,
		OnShutdown:    app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
