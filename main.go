package main

import (
	"embed"
	"log"
	"os"

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
	mutex, ok := core.AcquireSingleInstance(root)
	if !ok {
		return
	}
	defer core.ReleaseSingleInstance(mutex)

	paths := core.NewPaths(root)
	if err := paths.Ensure(); err != nil {
		log.Fatalf("failed to initialize folders: %v", err)
	}
	st := core.LoadSettings(root)

	startHidden := st.Get().StartMinimized
	for _, arg := range os.Args[1:] {
		if arg == "--minimized" || arg == "-m" {
			startHidden = true
		}
	}

	app := NewApp(paths, st)

	err := wails.Run(&options.App{
		Title:            "Darius DB (Lite Version 0.1.2)",
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
