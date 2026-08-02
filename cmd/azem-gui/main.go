//go:build darwin || windows

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	azemfrontend "github.com/Viking602/azem/frontend"
	azemapp "github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

var (
	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var configFile string
	var showVersion bool
	flag.StringVar(&configFile, "config", "", "path to config.yaml")
	flag.BoolVar(&showVersion, "version", false, "print version")
	flag.Parse()
	if showVersion {
		fmt.Printf("azem-gui %s\ngit commit: %s\nbuild time: %s\n", version, gitCommit, buildTime)
		return nil
	}
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get startup workspace: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	boot, err := azemapp.Bootstrap(ctx, workspace, configFile)
	if err != nil {
		cancel()
		return err
	}
	if err := boot.Validate(); err != nil {
		cancel()
		return err
	}

	var bridge *desktop.Bridge
	var mainWindow *application.WebviewWindow
	desktopApp := application.New(application.Options{
		Name: "Azem", Description: "Project-first agent workspace",
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(azemfrontend.Assets)},
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.viking602.azem",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if mainWindow != nil {
					mainWindow.Restore()
					mainWindow.Focus()
				}
			},
		},
		OnShutdown: func() {
			if bridge != nil {
				bridge.Close()
			}
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shutdownCancel()
			_ = boot.Service.Shutdown(shutdownCtx)
			cancel()
		},
	})
	bridge = desktop.NewBridge(ctx, boot, desktopApp.Event.Emit)
	desktopApp.RegisterService(application.NewService(bridge))
	mainWindow = desktopApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: "Azem", Width: 1440, Height: 920,
		MinWidth: 880, MinHeight: 640, URL: "/",
		BackgroundColour: application.NewRGB(245, 245, 243),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 46,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})
	return desktopApp.Run()
}
