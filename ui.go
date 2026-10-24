// Local UI extension, 2026-10-19. Official login remains in Claude and Chrome.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type pendingLogin struct {
	ProfileID     string `json:"profileID"`
	ExpectedEmail string `json:"expectedEmail"`
}

type uiSettings struct {
	Bindings map[string]string `json:"bindings"`
	Pending  *pendingLogin     `json:"pending,omitempty"`
}

type UI struct {
	s        *switcher
	chrome   chromeEnvironment
	mu       sync.Mutex
	settings uiSettings
}

type AccountView struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Email           string    `json:"email"`
	Plan            string    `json:"plan"`
	Usage           *Usage    `json:"usage"`
	Problem         string    `json:"problem"`
	ChromeProfileID string    `json:"chromeProfileID"`
	NextCheckAt     time.Time `json:"nextCheckAt"`
	RateLimited     bool      `json:"rateLimited"`
	NeedsChatSync   bool      `json:"needsChatSync"`
}

type Dashboard struct {
	Accounts        []AccountView   `json:"accounts"`
	Profiles        []ChromeProfile `json:"profiles"`
	CurrentID       string          `json:"currentID"`
	CurrentVerified bool            `json:"currentVerified"`
	ChromeAvailable bool            `json:"chromeAvailable"`
	ProfileError    string          `json:"profileError"`
	Busy            bool            `json:"busy"`
	Status          string          `json:"status"`
	Phase           string          `json:"phase"`
	RefreshError    string          `json:"refreshError"`
	Automatic       bool            `json:"automatic"`
	Pending         *pendingLogin   `json:"pending"`
	EasyAIAvailable bool            `json:"easyAIAvailable"`
	NextCheckAt     time.Time       `json:"nextCheckAt"`
	RateLimited     bool            `json:"rateLimited"`
	CheckBlocked    bool            `json:"checkBlocked"`
	Checking        bool            `json:"checking"`
	RateLimitUntil  time.Time       `json:"rateLimitUntil"`
	IdentityRetryAt time.Time       `json:"identityRetryAt"`
	UsageRetryAt    time.Time       `json:"usageRetryAt"`
}

func newUI(s *switcher, chrome chromeEnvironment) (*UI, error) {
	u := &UI{s: s, chrome: chrome, settings: uiSettings{Bindings: map[string]string{}}}
	data, err := os.ReadFile(u.settingsPath())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(data, &u.settings); err != nil {
			return nil, fmt.Errorf("界面配置无法读取，请保留 ui-settings.json 并检查文件：%w", err)
		}
	}
	if u.settings.Bindings == nil {
		u.settings.Bindings = map[string]string{}
	}
	s.loginPending.Store(u.settings.Pending != nil)
	return u, nil
}

func (u *UI) settingsPath() string { return filepath.Join(u.s.dir, "ui-settings.json") }
func (u *UI) saveSettingsLocked() error {
	data, err := json.MarshalIndent(u.settings, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(u.settingsPath(), data)
}

func (u *UI) GetDashboard() Dashboard {
	snap := u.s.snapshot()
	active, activeErr := u.s.d.activeAccount()
	profiles, err := readChromeProfiles(u.chrome.UserDataDir)
	state := Dashboard{Accounts: []AccountView{}, Profiles: profiles, CurrentID: active, CurrentVerified: activeErr == nil && active != "", ChromeAvailable: u.chrome.Executable != "", Busy: snap.Busy, Status: snap.Status, Phase: snap.Phase, RefreshError: snap.RefreshError, Automatic: snap.AutoSwitch || snap.SpendWeekly, EasyAIAvailable: fileExists(easyAIPath())}
	state.NextCheckAt = snap.NextChecks[active]
	state.RateLimitUntil = snap.RateLimitedUntil[active]
	state.RateLimited = state.RateLimitUntil.After(time.Now())
	if at := snap.ProfileChecks[active]; !at.IsZero() {
		state.IdentityRetryAt = at.Add(identityMinGap)
	}
	if at := snap.UsageChecks[active]; !at.IsZero() {
		state.UsageRetryAt = at.Add(identityMinGap)
	}
	state.CheckBlocked = snap.CheckPending[active] && !snap.Checking
	state.Checking = snap.Checking
	if err != nil {
		state.ProfileError = err.Error()
		state.Profiles = []ChromeProfile{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.settings.Pending != nil {
		copy := *u.settings.Pending
		state.Pending = &copy
	}
	for _, a := range snap.Accounts {
		view := AccountView{ID: a.UUID, Name: a.Name, Email: a.Email, Plan: a.Plan, Problem: a.Problem, ChromeProfileID: u.settings.Bindings[a.UUID]}
		view.NextCheckAt = snap.NextChecks[a.UUID]
		view.RateLimited = snap.RateLimitedUntil[a.UUID].After(time.Now())
		view.NeedsChatSync = a.NeedsChatSync
		if a.Usage != nil {
			usage := a.Usage.current(time.Now())
			view.Usage = &usage
		}
		state.Accounts = append(state.Accounts, view)
	}
	return state
}

func (u *UI) knownAccount(id string) (Account, bool) {
	for _, a := range u.s.snapshot().Accounts {
		if a.UUID == id {
			return a, true
		}
	}
	return Account{}, false
}
func (u *UI) checkProfile(id string) error {
	_, err := chromeProfileArgs(u.chrome.UserDataDir, id)
	return err
}

func (u *UI) checkBindingLocked(accountID, profileID string) error {
	for owner, p := range u.settings.Bindings {
		if owner != accountID && p == profileID {
			return errors.New("这个 Chrome 个人资料已绑定另一个账号，请为不同账号使用不同个人资料")
		}
	}
	return nil
}

func (u *UI) BindChromeProfile(accountID, profileID string) error {
	if u.s.busy.Load() {
		return errors.New("正在执行操作，请稍后修改绑定")
	}
	if _, ok := u.knownAccount(accountID); !ok {
		return errors.New("未找到此账号")
	}
	if err := u.checkProfile(profileID); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := u.checkBindingLocked(accountID, profileID); err != nil {
		return err
	}
	previous, existed := u.settings.Bindings[accountID]
	u.settings.Bindings[accountID] = profileID
	if err := u.saveSettingsLocked(); err != nil {
		if existed {
			u.settings.Bindings[accountID] = previous
		} else {
			delete(u.settings.Bindings, accountID)
		}
		return err
	}
	return nil
}

func (u *UI) OpenProfile(profileID string) error { return u.chrome.open(profileID) }
func (u *UI) OpenAccountChrome(id string) error {
	if _, ok := u.knownAccount(id); !ok {
		return errors.New("未找到此账号")
	}
	u.mu.Lock()
	profile := u.settings.Bindings[id]
	u.mu.Unlock()
	if profile == "" {
		return errors.New("请先为此账号绑定 Chrome 个人资料")
	}
	return u.chrome.open(profile)
}

func (u *UI) enqueue(name string, action func() error) error {
	if !u.s.busy.CompareAndSwap(false, true) {
		return errors.New("已有操作正在进行，请等待完成")
	}
	u.s.setPhase("preparing", name+"：正在准备")
	go u.s.runReserved(name, action)
	return nil
}

func (u *UI) StartAddAccount(profileID, email string, networkConfirmed bool) error {
	snap := u.s.snapshot()
	if snap.AutoSwitch || snap.SpendWeekly {
		return errors.New("请先切为手动模式，再添加或重新登录账号")
	}
	if !networkConfirmed {
		return errors.New("请先确认当前网络可访问 Claude，且所选 Chrome 资料用于目标账号")
	}
	email = strings.TrimSpace(email)
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return errors.New("请填写目标 Claude 账号的完整邮箱，用于完成登录后核对")
	}
	if err = u.checkProfile(profileID); err != nil {
		return err
	}
	if u.chrome.Executable == "" {
		return errors.New("未找到 Google Chrome")
	}
	u.mu.Lock()
	if u.settings.Pending != nil {
		u.mu.Unlock()
		return errors.New("已有待完成的登录，请先核对完成或取消")
	}
	owners := map[string]string{}
	for owner, p := range u.settings.Bindings {
		owners[owner] = p
	}
	u.mu.Unlock()
	for owner, p := range owners {
		if p == profileID {
			a, ok := u.knownAccount(owner)
			if !ok || !strings.EqualFold(a.Email, email) {
				return errors.New("所选 Chrome 资料属于另一个账号，请重新选择")
			}
		}
	}
	return u.enqueue("添加账号", func() error {
		u.mu.Lock()
		u.settings.Pending = &pendingLogin{ProfileID: profileID, ExpectedEmail: email}
		err := u.saveSettingsLocked()
		if err != nil {
			u.settings.Pending = nil
		}
		u.s.loginPending.Store(u.settings.Pending != nil)
		u.mu.Unlock()
		if err != nil {
			return err
		}
		if err = u.chrome.open(profileID); err == nil {
			err = u.s.addAccount()
		}
		if err != nil {
			u.mu.Lock()
			u.settings.Pending = nil
			if saveErr := u.saveSettingsLocked(); saveErr != nil {
				u.settings.Pending = &pendingLogin{ProfileID: profileID, ExpectedEmail: email}
				err = errors.Join(err, saveErr)
			}
			u.s.loginPending.Store(u.settings.Pending != nil)
			u.mu.Unlock()
			return err
		}
		return nil
	})
}

func (u *UI) CompleteLogin(syncConfirmed bool) error {
	if !u.s.busy.CompareAndSwap(false, true) {
		return errors.New("当前操作尚未完成，请稍后核对")
	}
	u.s.refreshMu.Lock()
	defer func() { u.s.refreshMu.Unlock(); u.s.busy.Store(false); u.s.onChange() }()
	active, err := u.s.d.activeAccount()
	if err != nil {
		return err
	}
	a, ok := u.knownAccount(active)
	if !ok {
		return errors.New("尚未识别到当前登录账号，请完成官方登录并稍等片刻，或检查网络后点击重新识别账号")
	}
	u.mu.Lock()
	pending := u.settings.Pending
	u.mu.Unlock()
	if pending == nil {
		return errors.New("没有待完成的登录")
	}
	if !strings.EqualFold(strings.TrimSpace(a.Email), pending.ExpectedEmail) {
		return fmt.Errorf("当前识别到 %s，与目标 %s 不一致；尚未保存绑定，请核对 Claude 和 Chrome 账号", a.Email, pending.ExpectedEmail)
	}
	if err = u.checkProfile(pending.ProfileID); err != nil {
		return err
	}
	u.mu.Lock()
	err = u.checkBindingLocked(a.UUID, pending.ProfileID)
	u.mu.Unlock()
	if err != nil {
		return err
	}
	if a.NeedsChatSync {
		if !syncConfirmed {
			return errors.New("此账号还需同步原聊天。请先保存并停止 Claude 任务，再确认同步并保存")
		}
		if err = u.s.importChats(); err != nil {
			u.s.setPhase("error", "聊天同步未完成："+err.Error())
			return err
		}
		actual, readErr := u.s.d.activeAccount()
		if readErr != nil {
			return readErr
		}
		if actual != a.UUID {
			return errors.New("同步后账号发生变化，请重新核对")
		}
		u.s.mu.Lock()
		u.s.account(a.UUID).NeedsChatSync = false
		err = u.s.saveState()
		if err != nil {
			u.s.account(a.UUID).NeedsChatSync = true
		}
		u.s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err = u.checkBindingLocked(a.UUID, pending.ProfileID); err != nil {
		return err
	}
	previous, existed := u.settings.Bindings[a.UUID]
	u.settings.Bindings[a.UUID] = pending.ProfileID
	u.settings.Pending = nil
	if err = u.saveSettingsLocked(); err != nil {
		u.settings.Pending = pending
		if existed {
			u.settings.Bindings[a.UUID] = previous
		} else {
			delete(u.settings.Bindings, a.UUID)
		}
		return err
	}
	u.s.mu.Lock()
	u.s.addingAt = time.Time{}
	u.s.mu.Unlock()
	u.s.loginPending.Store(false)
	u.s.setPhase("done", "账号邮箱核对一致，Chrome 资料绑定已保存")
	return nil
}

func (u *UI) CancelLogin() error {
	if u.s.busy.Load() {
		return errors.New("请等待当前操作完成")
	}
	u.mu.Lock()
	previous := u.settings.Pending
	u.settings.Pending = nil
	err := u.saveSettingsLocked()
	if err != nil {
		u.settings.Pending = previous
	}
	u.mu.Unlock()
	if err != nil {
		return err
	}
	u.s.loginPending.Store(false)
	u.s.mu.Lock()
	u.s.addingAt = time.Time{}
	u.s.mu.Unlock()
	u.s.setPhase("idle", "已取消登录引导；可切回已保存的账号")
	return nil
}

func (u *UI) SwitchAccount(id string, confirmed bool) error {
	u.mu.Lock()
	pending := u.settings.Pending != nil
	u.mu.Unlock()
	if pending {
		return errors.New("请先完成或取消当前登录引导，再切换账号")
	}
	if !confirmed {
		return errors.New("切换会重启 Claude，请先保存并停止当前任务")
	}
	if _, ok := u.knownAccount(id); !ok {
		return errors.New("未找到此账号")
	}
	return u.enqueue("切换账号", func() error { return u.s.switchTo(id, byHand) })
}
func (u *UI) SyncChats(confirmed bool) error {
	if !confirmed {
		return errors.New("同步会重启 Claude，请先保存并停止当前任务")
	}
	return u.enqueue("同步聊天", u.s.importChats)
}
func (u *UI) RefreshUsage() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return u.s.refreshCurrentUsage(ctx)
}

func (u *UI) RefreshIdentity() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	email, err := u.s.identifyCurrent(ctx)
	if err != nil {
		return "", err
	}
	return "已识别当前账号：" + email, nil
}
func (u *UI) UseManualSwitching() { u.s.setAuto(false); u.s.setSpendWeekly(false) }
func (u *UI) OpenClaude() error {
	if u.s.busy.Load() {
		return errors.New("当前操作尚未完成")
	}
	u.s.mu.Lock()
	exe := u.s.st.AppExe
	u.s.mu.Unlock()
	return startDesktop(exe)
}
func (u *UI) OpenDataFolder() { shellOpen(u.s.dir) }
func easyAIPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "easyAI", "easyAI.exe")
}
func fileExists(path string) bool { info, err := os.Stat(path); return err == nil && !info.IsDir() }
func (u *UI) OpenEasyAI() error {
	path := easyAIPath()
	if !fileExists(path) {
		return errors.New("未在默认位置找到 easyAI，请从原有快捷方式打开")
	}
	return exec.Command(path).Start()
}
