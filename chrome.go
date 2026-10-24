// Local UI extension, 2026-10-19. Chrome metadata only; no browser credentials.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type chromeEnvironment struct {
	UserDataDir string
	Executable  string
}

type ChromeProfile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func defaultChromeEnvironment() chromeEnvironment {
	env := chromeEnvironment{UserDataDir: filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "User Data")}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
		if root == "" {
			continue
		}
		path := filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			env.Executable = path
			break
		}
	}
	return env
}

func validChromeProfileID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/\\:\x00") && id != "Guest Profile" && id != "System Profile" && filepath.Base(id) == id
}

func readChromeProfiles(root string) ([]ChromeProfile, error) {
	data, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("无法读取 Chrome 个人资料，请先打开一次 Chrome：%w", err)
	}
	var state struct {
		Profile struct {
			Info map[string]struct {
				Name  string `json:"name"`
				Email string `json:"user_name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("Chrome 个人资料信息无法解析：%w", err)
	}
	profiles := []ChromeProfile{}
	for id, item := range state.Profile.Info {
		if !validChromeProfileID(id) {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, id)); err != nil || !info.IsDir() {
			continue
		}
		name := item.Name
		if name == "" {
			name = id
		}
		profiles = append(profiles, ChromeProfile{ID: id, Name: name, Email: item.Email})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	return profiles, nil
}

func chromeProfileArgs(root, id string) ([]string, error) {
	if !validChromeProfileID(id) {
		return nil, errors.New("Chrome 个人资料目录无效")
	}
	profiles, err := readChromeProfiles(root)
	if err != nil {
		return nil, err
	}
	for _, p := range profiles {
		if p.ID == id {
			return []string{"--user-data-dir=" + root, "--profile-directory=" + id, "--new-window", "about:blank"}, nil
		}
	}
	return nil, errors.New("未找到这个 Chrome 个人资料，请刷新列表后重新选择")
}

func (env chromeEnvironment) open(id string) error {
	if env.Executable == "" {
		return errors.New("未找到 Google Chrome，请先安装官方 Chrome")
	}
	args, err := chromeProfileArgs(env.UserDataDir, id)
	if err != nil {
		return err
	}
	return exec.Command(env.Executable, args...).Start()
}
