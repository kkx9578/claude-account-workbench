//go:build windows

// Local UI extension, 2026-10-19. Embedded assets; no embedded account login.
package main

import (
	"context"
	"embed"
	"io/fs"
	"path/filepath"
	"sync"
	"unsafe"

	"fyne.io/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	winoptions "github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

//go:embed frontend/*
var uiAssets embed.FS

func runDesktopUI(s *switcher) error {
	u, err := newUI(s, defaultChromeEnvironment())
	if err != nil {
		return err
	}
	assets, err := fs.Sub(uiAssets, "frontend")
	if err != nil {
		return err
	}
	var contextMu sync.Mutex
	var windowContext context.Context
	withContext := func(action func(context.Context)) {
		contextMu.Lock()
		ctx := windowContext
		contextMu.Unlock()
		if ctx != nil {
			action(ctx)
		}
	}
	t := &tray{s: s, updates: make(chan struct{}, 1)}
	t.openWindow = func() {
		withContext(func(ctx context.Context) { wruntime.WindowShow(ctx); wruntime.WindowUnminimise(ctx) })
	}
	t.exitApp = func() { withContext(wruntime.Quit) }
	t.onAdd = func() {
		t.openWindow()
		withContext(func(ctx context.Context) { wruntime.EventsEmit(ctx, "tray-add") })
	}
	t.onSwitch = func(id string) {
		t.openWindow()
		withContext(func(ctx context.Context) { wruntime.EventsEmit(ctx, "tray-switch", id) })
	}
	t.onSync = func() {
		t.openWindow()
		withContext(func(ctx context.Context) { wruntime.EventsEmit(ctx, "tray-sync") })
	}
	// Register integrates the existing tray into Wails' Windows message loop.
	systray.Register(t.onReady, func() {})
	defer systray.Quit()
	return wails.Run(&options.App{
		Title: "Claude 账号 · 简洁版", Width: 960, Height: 680, MinWidth: 780, MinHeight: 600,
		HideWindowOnClose: true, BackgroundColour: &options.RGBA{R: 247, G: 248, B: 245, A: 255},
		AssetServer: &assetserver.Options{Assets: assets}, Bind: []interface{}{u},
		OnStartup: func(ctx context.Context) { contextMu.Lock(); windowContext = ctx; contextMu.Unlock() },
		OnShutdown: func(context.Context) {
			s.shuttingDown.Store(true)
			s.refreshMu.Lock()
			s.refreshMu.Unlock()
			systray.Quit()
		},
		Windows: &winoptions.Options{Theme: winoptions.Light, WebviewUserDataPath: filepath.Join(s.dir, "ui-webview")},
	})
}

func showStartupError(message string) {
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("Claude 账号工作台")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x30)
}
