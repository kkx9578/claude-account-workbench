// Command claude-account-switcher is a tray tool for the Claude desktop app.
// It switches the app between signed-in accounts without signing in again,
// keeps Claude Code chats visible in every account and shows plan usage.
// Modified 2026-10-19: Chinese UI, Chrome bindings and startup error display.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"
)

const version = "0.1.0"

// Only one switcher may run; a second start explains how to open the tray UI.
const instancePort = "127.0.0.1:47823"

func main() {
	status := flag.Bool("status", false, "只读输出本地缓存的账号和用量后退出")
	flag.Parse()

	// The home folder, because the Store app redirects AppData for everything
	// it starts but leaves the home folder alone.
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	dir := filepath.Join(home, ".claude-account-switcher")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatal(err)
	}
	setupLog(filepath.Join(dir, "switcher.log"), *status)

	if *status {
		printStatus(newSwitcher(dir))
		return
	}

	if relaunchOutsideDesktop(dir) {
		return
	}

	lock, err := net.Listen("tcp", instancePort)
	if err != nil {
		showStartupError("账号切换器已经在运行。请从托盘打开工作台；若仍是旧版，请先退出旧版再运行新版。")
		return
	}
	defer lock.Close()

	log.Printf("claude-account-switcher %s, app data in %s", version, claudeDesktop().dir)
	if err := runDesktopUI(newSwitcher(dir)); err != nil {
		log.Print(err)
		showStartupError(err.Error())
	}
}

// setupLog writes to switcher.log and starts it over once it passes 1 MB.
// A tray app has no usable stderr on Windows, so only -status also prints there.
func setupLog(path string, console bool) {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > 1<<20 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return
	}
	if console {
		log.SetOutput(io.MultiWriter(f, os.Stderr))
	} else {
		log.SetOutput(f)
	}
}

// printStatus only reads local metadata and cached usage; it never polls or switches.
func printStatus(s *switcher) {
	snap := s.snapshot()
	snap.Active, _ = s.d.activeAccount()
	if len(snap.Accounts) == 0 {
		fmt.Println("尚无账号，请先在 Claude 中登录")
	}
	for _, a := range snap.Accounts {
		mark := " "
		if a.UUID == snap.Active {
			mark = "*"
		}
		fmt.Printf("%s %s  %s\n", mark, a.label(), usageText(a, snap.Threshold, time.Now()))
	}
}
