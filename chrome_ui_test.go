package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func chromeFixture(t *testing.T) chromeEnvironment {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "Local State"), `{"os_crypt":{"encrypted_key":"DO-NOT-EXPOSE"},"profile":{"info_cache":{"Default":{"name":"日常","user_name":"a@example.com"},"Profile 2":{"name":"工作","user_name":"b@example.com"},"../escape":{"name":"invalid"},"Guest Profile":{"name":"Guest"}}}}`)
	for _, name := range []string{"Default", "Profile 2", "Guest Profile"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(t.TempDir(), "chrome.exe")
	write(t, exe, "test executable boundary")
	return chromeEnvironment{UserDataDir: root, Executable: exe}
}

func TestChromeDiscoveryRejectsPathsAndGuest(t *testing.T) {
	env := chromeFixture(t)
	profiles, err := readChromeProfiles(env.UserDataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || profiles[0].ID != "Default" || profiles[1].ID != "Profile 2" {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}
	args, err := chromeProfileArgs(env.UserDataDir, "Profile 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 4 || args[0] != "--user-data-dir="+env.UserDataDir || args[1] != "--profile-directory=Profile 2" || args[2] != "--new-window" || args[3] != "about:blank" {
		t.Fatalf("wrong launch arguments: %q", args)
	}
	for _, id := range []string{"../escape", `..\escape`, `C:\other`, "Profile 999", "Guest Profile", ""} {
		if _, err := chromeProfileArgs(env.UserDataDir, id); err == nil {
			t.Errorf("accepted profile %q", id)
		}
	}
}

func uiFixture(t *testing.T) *UI {
	t.Helper()
	s := newSwitcher(t.TempDir())
	s.d = desktop{dir: t.TempDir()}
	s.st.Accounts = []*Account{{UUID: "a", Org: "oa", Email: "a@example.com"}, {UUID: "b", Org: "ob", Email: "b@example.com"}}
	u, err := newUI(s, chromeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestChromeBindingsPersistAndCannotBeShared(t *testing.T) {
	u := uiFixture(t)
	if err := u.BindChromeProfile("a", "Default"); err != nil {
		t.Fatal(err)
	}
	if err := u.BindChromeProfile("b", "Default"); err == nil {
		t.Fatal("same Chrome profile assigned to two accounts")
	}
	if err := u.BindChromeProfile("b", "Profile 2"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newUI(u.s, u.chrome)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.settings.Bindings["a"] != "Default" || reloaded.settings.Bindings["b"] != "Profile 2" {
		t.Fatalf("bindings lost: %+v", reloaded.settings)
	}
	if err := u.BindChromeProfile("missing", "Default"); err == nil {
		t.Fatal("accepted unknown account")
	}
	if err := u.BindChromeProfile("a", "../escape"); err == nil {
		t.Fatal("accepted invalid profile path")
	}
}

func TestLoginCompletionRejectsWrongEmail(t *testing.T) {
	u := uiFixture(t)
	write(t, u.s.d.configPath(), `{"lastKnownAccountUuid":"b","oauth:tokenCacheV2":"fixture-b"}`)
	u.s.active = "b"
	u.settings.Pending = &pendingLogin{ProfileID: "Default", ExpectedEmail: "a@example.com"}
	if err := u.CompleteLogin(false); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatal("bound a login with a different email")
	}
	if len(u.settings.Bindings) != 0 || u.settings.Pending == nil {
		t.Fatal("wrong-account check changed bindings or discarded pending login")
	}
	write(t, u.s.d.configPath(), `{"lastKnownAccountUuid":"a","oauth:tokenCacheV2":"fixture-a"}`)
	u.s.active = "a"
	u.s.addingAt = time.Now()
	if err := u.CompleteLogin(false); err != nil {
		t.Fatal(err)
	}
	if u.settings.Bindings["a"] != "Default" || u.settings.Pending != nil {
		t.Fatal("successful login was not bound")
	}
	if u.s.adding() {
		t.Fatal("completed re-login left engine waiting for login")
	}
	for _, a := range u.s.st.Accounts {
		a.Usage = &Usage{CheckedAt: time.Now()}
	}
	u.s.run("next operation", func() error { return nil })
	if u.s.snapshot().Phase != "done" {
		t.Fatal("next operation still reports login")
	}
}

func TestPendingLoginRestoredFromDiskBlocksAutomation(t *testing.T) {
	u := uiFixture(t)
	u.settings.Pending = &pendingLogin{ProfileID: "Default", ExpectedEmail: "a@example.com"}
	if err := u.saveSettingsLocked(); err != nil {
		t.Fatal(err)
	}
	u, err := newUI(u.s, u.chrome)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	u.s.active = "a"
	u.s.st.Accounts[0].Usage = &Usage{Session: 99, CheckedAt: now}
	u.s.st.Accounts[1].Usage = &Usage{Session: 10, CheckedAt: now}
	u.s.setAuto(true)
	u.s.setSpendWeekly(true)
	if target, _ := u.s.autoTarget(true, time.Time{}, now); target != nil {
		t.Fatal("pending login allowed automatic switching")
	}
	if err := u.CancelLogin(); err != nil {
		t.Fatal(err)
	}
	if target, _ := u.s.autoTarget(true, time.Time{}, now); target == nil {
		t.Fatal("cancel did not release automatic switching guard")
	}
}

func TestDashboardUsesUnknownUsageAndOmitsCredentials(t *testing.T) {
	u := uiFixture(t)
	write(t, u.s.d.configPath(), `{"lastKnownAccountUuid":"a","oauth:tokenCacheV2":"SECRET-TOKEN"}`)
	state := u.GetDashboard()
	if len(state.Accounts) != 2 || state.Accounts[0].Usage != nil {
		t.Fatal("missing usage was converted to a number")
	}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRET-TOKEN") || strings.Contains(string(b), "DO-NOT-EXPOSE") {
		t.Fatal("credentials escaped into UI payload")
	}
}

func TestInvalidUISettingsAreNotOverwritten(t *testing.T) {
	s := newSwitcher(t.TempDir())
	path := filepath.Join(s.dir, "ui-settings.json")
	write(t, path, "{broken")
	if _, err := newUI(s, chromeFixture(t)); err == nil {
		t.Fatal("accepted corrupt settings")
	}
	if read(t, path) != "{broken" {
		t.Fatal("overwrote corrupt settings")
	}
}

func TestDuplicateActionsAreRejectedWhileBusy(t *testing.T) {
	u := uiFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{}, 1)
	u.s.onChange = func() {
		if u.s.snapshot().Phase == "error" {
			select {
			case finished <- struct{}{}:
			default:
			}
		}
	}
	if err := u.enqueue("first", func() error { close(entered); <-release; return errors.New("fixture boundary") }); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := u.enqueue("second", func() error { t.Error("duplicate action ran"); return nil }); err == nil {
		t.Error("accepted duplicate action")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("action did not finish")
	}
}

func TestPendingLoginWriteFailureDoesNotOpenOrClearLogin(t *testing.T) {
	u := uiFixture(t)
	if err := os.Mkdir(u.settingsPath(), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, u.s.d.configPath(), `{"lastKnownAccountUuid":"a","oauth:tokenCacheV2":"keep-a"}`)
	finished := make(chan struct{}, 1)
	u.s.onChange = func() {
		if u.s.snapshot().Phase == "error" {
			select {
			case finished <- struct{}{}:
			default:
			}
		}
	}
	if err := u.StartAddAccount("Default", "a@example.com", true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("write failure did not finish")
	}
	if u.settings.Pending != nil {
		t.Fatal("failed pending login retained")
	}
	if got := read(t, u.s.d.configPath()); got != `{"lastKnownAccountUuid":"a","oauth:tokenCacheV2":"keep-a"}` {
		t.Fatal("write failure altered Claude login")
	}
}

func TestPendingLoginBlocksSwitchAndAutomaticModeBlocksLogin(t *testing.T) {
	u := uiFixture(t)
	u.settings.Pending = &pendingLogin{ExpectedEmail: "a@example.com", ProfileID: "Default"}
	if err := u.SwitchAccount("b", true); err == nil {
		t.Fatal("switched while login confirmation was pending")
	}
	u.settings.Pending = nil
	u.s.st.AutoSwitch = true
	if err := u.StartAddAccount("Default", "a@example.com", true); err == nil {
		t.Fatal("started login while automatic switching was enabled")
	}
}
