package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func continuityFixture(t *testing.T) *switcher {
	t.Helper()
	s := newSwitcher(t.TempDir())
	s.d = desktop{dir: t.TempDir()}
	s.st.Accounts = []*Account{{UUID: "a", Org: "oa", Email: "a@example.com"}, {UUID: "b", Org: "ob", Email: "b@example.com"}}
	s.active = "a"
	write(t, s.d.configPath(), sampleConfig)
	write(t, s.d.cookieFiles()[0], "cookies-a")
	write(t, filepath.Join(s.profileDir("b"), "config.json"), `{"lastKnownAccountUuid":"b","oauth:tokenCacheV2":"saved-b"}`)
	write(t, filepath.Join(s.profileDir("b"), "Cookies"), "cookies-b")
	s.quitApp = func() error { return nil }
	s.startApp = func(string) error { return nil }
	return s
}

func TestSwitchKeepsSameChatPointerAndProject(t *testing.T) {
	s := continuityFixture(t)
	project := t.TempDir()
	code := filepath.Join(project, "main.py")
	write(t, code, "print('shared')")
	transcript := filepath.Join(project, "history.jsonl")
	write(t, transcript, "existing transcript")
	entry := `{"sessionId":"local_one","cliSessionId":"same-conversation","cwd":"same-project","lastActivityAt":100}`
	write(t, filepath.Join(s.d.sessionsDir(), "a", "oa", "local_one.json"), entry)
	if err := s.switchTo("b", byHand); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(s.d.sessionsDir(), "b", "ob", "local_one.json")); got != entry {
		t.Fatal("session identity changed")
	}
	if read(t, code) != "print('shared')" || read(t, transcript) != "existing transcript" {
		t.Fatal("project or conversation body changed")
	}
	if current, _ := s.d.activeAccount(); current != "b" {
		t.Fatalf("wrong account: %s", current)
	}
}

func TestRestartLaunchFailureIsNotReportedAsSuccess(t *testing.T) {
	s := continuityFixture(t)
	s.startApp = func(string) error { return errors.New("launch failed") }
	s.run("切换账号", func() error { return s.switchTo("b", byHand) })
	if s.snapshot().Phase != "error" || s.snapshot().Status == "" {
		t.Fatalf("launch failure lost: %+v", s.snapshot())
	}
}
