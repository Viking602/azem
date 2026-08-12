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
	"github.com/wailsapp/wails/v3/pkg/events"
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
	var configFile, initialSession, workspaceOverride string
	initialSearchSequence := int64(-1)
	var showVersion, newWindow bool
	flag.StringVar(&configFile, "config", "", "path to config.yaml")
	flag.StringVar(&initialSession, "session", "", "session to open")
	flag.Int64Var(&initialSearchSequence, "search-sequence", -1, "session block sequence to focus")
	flag.StringVar(&workspaceOverride, "workspace", "", "workspace to open")
	flag.BoolVar(&newWindow, "new-window", false, "open an independent window")
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
	boot, err := bootstrapDesktop(ctx, workspace, workspaceOverride, configFile)
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
	var windowTracker *windowStateTracker
	var handleDeepLink func(string)
	options := application.Options{
		Name: "Azem", Description: "Project-first agent workspace",
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(azemfrontend.Assets)},
		Mac:    desktopMacOptions(newWindow),
		OnShutdown: func() {
			if windowTracker != nil {
				windowTracker.Flush()
			}
			if bridge != nil {
				bridge.Close()
			}
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shutdownCancel()
			_ = boot.Service.Shutdown(shutdownCtx)
			cancel()
		},
	}
	options.SingleInstance = sessionSingleInstance(newWindow, &mainWindow, &handleDeepLink)
	desktopApp := application.New(options)
	bridge = desktop.NewBridge(ctx, boot, desktopApp.Event.Emit, func(workspace, sessionID string, sequence int64) error {
		return launchSessionWindow(configFile, sessionID, workspace, true, sequence)
	})
	desktopApp.RegisterService(application.NewService(bridge))
	geometry := loadMainWindowGeometry(desktopApp, boot.Paths.StateDir)
	windowOptions := application.WebviewWindowOptions{
		Name: "main", Title: "Azem",
		URL:              sessionWindowURL(initialSession, initialSearchSequence),
		BackgroundColour: application.NewRGB(245, 245, 243),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 46,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	}
	applyWindowGeometry(&windowOptions, geometry)
	mainWindow = desktopApp.Window.NewWithOptions(windowOptions)
	windowTracker = newWindowStateTracker(desktop.WindowStatePath(boot.Paths.StateDir), desktopApp, geometry)
	bindWindowStatePersistence(mainWindow, windowTracker)
	registerSessionContextMenus(desktopApp, bridge, boot.Paths.Workspace, boot.Paths.DataDir, configFile, boot.Config.Defaults.Language)
	handleDeepLink = sessionDeepLinkHandler(bridge, mainWindow, boot.Paths.Workspace)
	desktopApp.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(event *application.ApplicationEvent) {
		handleDeepLink(event.Context().URL())
	})
	return desktopApp.Run()
}

func desktopMacOptions(independentWindow bool) application.MacOptions {
	options := application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true}
	if independentWindow {
		// Project and session windows keep an isolated runtime so work in another
		// workspace can continue, but they are windows of the existing Azem app
		// from the user's perspective. Accessory processes do not create another
		// Dock or Cmd-Tab application entry on macOS.
		options.ActivationPolicy = application.ActivationPolicyAccessory
	}
	return options
}

func bootstrapDesktop(ctx context.Context, startupWorkspace, workspaceOverride, configFile string) (azemapp.BootstrapResult, error) {
	if workspaceOverride != "" {
		return azemapp.BootstrapDesktopAtWorkspace(ctx, workspaceOverride, configFile)
	}
	return azemapp.BootstrapDesktop(ctx, startupWorkspace, configFile)
}

func sessionSingleInstance(newWindow bool, mainWindow **application.WebviewWindow, handleDeepLink *func(string)) *application.SingleInstanceOptions {
	if newWindow {
		return nil
	}
	return &application.SingleInstanceOptions{
		UniqueID: "com.viking602.azem",
		OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
			if *handleDeepLink != nil {
				if raw := sessionURLFromArgs(data.Args); raw != "" {
					(*handleDeepLink)(raw)
				}
			}
			if *mainWindow != nil {
				(*mainWindow).Restore()
				(*mainWindow).Focus()
			}
		},
	}
}
