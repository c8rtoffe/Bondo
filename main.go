package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	args := os.Args[1:]
	if handled, err := handleUnregisterCommand(args, func() error {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		return unregisterMagnetProtocol(executable)
	}); handled {
		if err != nil {
			log.Fatalf("Cannot unregister Bondo magnet links: %v", err)
		}
		return
	}
	magnet, err := magnetFromArgs(args)
	if err != nil {
		log.Fatal(err)
	}

	app := NewApp()

	app.initialMagnet = magnet

	err = wails.Run(&options.App{
		Title:             "Bondo - Multipath BitTorrent Accelerator",
		Width:             860,
		Height:            580,
		MinWidth:          640,
		MinHeight:         460,
		DisableResize:     false,
		Fullscreen:        false,
		Frameless:         false,
		StartHidden:       false,
		HideWindowOnClose: false,
		BackgroundColour:  &options.RGBA{R: 15, G: 23, B: 42, A: 255},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.Startup,
		OnShutdown: app.Shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               singleInstanceID,
			OnSecondInstanceLaunch: app.onSecondInstanceLaunch,
		},
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			BackdropType:         windows.Mica,
			Theme:                windows.Dark,
		},
	})

	if err != nil {
		log.Fatalf("Error running Bondo desktop application: %v", err)
	}
}
