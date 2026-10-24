package main

import (
	"os"
	"testing"
	"time"
)

func TestStatusOnlyReadsCachedState(t *testing.T) {
	s := pollingFixture(t)
	s.st.AutoSwitch = true
	s.st.Accounts[0].Usage = &Usage{Session: 42, Weekly: 10, CheckedAt: time.Now().Add(-time.Hour)}
	s.tokenReader = func(string, string) (string, error) {
		t.Error("status tried to read an API token")
		return "", errSignedOut
	}
	s.quitApp = func() error { t.Error("status tried to stop Claude"); return nil }
	s.startApp = func(string) error { t.Error("status tried to start Claude"); return nil }
	if err := s.saveState(); err != nil {
		t.Fatal(err)
	}
	before := read(t, s.statePath())
	output, err := os.CreateTemp(t.TempDir(), "status-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous; output.Close() }()
	printStatus(s)
	if read(t, s.statePath()) != before {
		t.Fatal("status changed persisted state")
	}
}
