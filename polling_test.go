package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type quotaTransport func(*http.Request) (*http.Response, error)

func (f quotaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQuotaCooldownSurvivesRestart(t *testing.T) {
	s := newSwitcher(t.TempDir())
	until := time.Now().Add(45 * time.Minute).Truncate(time.Second)
	s.backoff["account-test"] = until
	s.strikes["account-test"] = 3
	s.saveState()
	loaded := newSwitcher(s.dir)
	if !loaded.backoff["account-test"].Equal(until) || loaded.strikes["account-test"] != 3 {
		t.Fatal("restart discarded rate-limit cooldown")
	}
}

func TestRetryAfterHTTPDateIsHonored(t *testing.T) {
	previous := client
	t.Cleanup(func() { client = previous })
	until := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	client = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{until.Format(http.TimeFormat)}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	err := apiGet(context.Background(), "synthetic", "/test", new(any))
	var limited *rateLimited
	if !errors.As(err, &limited) || limited.after < 59*time.Minute {
		t.Fatalf("HTTP-date ignored: %v", err)
	}
}

func TestKnownAccountUpgradeStartsQuietly(t *testing.T) {
	s := newSwitcher(t.TempDir())
	s.st.Accounts = []*Account{{UUID: "account-test", Usage: &Usage{CheckedAt: time.Now().Add(-time.Hour)}}}
	s.saveState()
	loaded := newSwitcher(s.dir)
	if time.Until(loaded.backoff["account-test"]) < 14*time.Minute {
		t.Fatal("upgrade immediately re-queries a previously limited account")
	}
}

func TestQuotaIntervalsStayConservative(t *testing.T) {
	if activeEvery < 15*time.Minute || idleEvery < 30*time.Minute {
		t.Fatal("routine quota polling is too frequent")
	}
}

func pollingFixture(t *testing.T) *switcher {
	t.Helper()
	s := newSwitcher(t.TempDir())
	s.d = desktop{dir: t.TempDir()}
	s.st.Accounts = []*Account{{UUID: "account-test", Org: "org-test", Email: "test@example.com"}}
	write(t, s.d.configPath(), `{"lastKnownAccountUuid":"account-test","oauth:tokenCacheV2":"synthetic"}`)
	s.tokenReader = func(string, string) (string, error) { return "synthetic-token", nil }
	return s
}

func TestHTTPFailuresDoNotRepeatAfterRefreshOrRestart(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			previous := client
			t.Cleanup(func() { client = previous })
			calls := 0
			client = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}
			s := pollingFixture(t)
			s.refresh(context.Background())
			for i := 0; i < 12; i++ {
				s.refresh(context.Background())
			}
			loaded := newSwitcher(s.dir)
			loaded.d = s.d
			loaded.tokenReader = s.tokenReader
			loaded.refresh(context.Background())
			if calls != 1 {
				t.Fatalf("refresh/restart made %d calls, want one", calls)
			}
			minimum := 14 * time.Minute
			if status == 429 {
				minimum = 29 * time.Minute
			}
			if time.Until(loaded.backoff["account-test"]) < minimum {
				t.Fatal("cooldown too short")
			}
		})
	}
}

func TestConcurrentRefreshesAreDroppedRatherThanQueued(t *testing.T) {
	previous := client
	t.Cleanup(func() { client = previous })
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	client = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"five_hour":{"utilization":99}}`))}, nil
	})}
	s := pollingFixture(t)
	go func() { s.refresh(context.Background()); close(finished) }()
	<-entered
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() { defer group.Done(); s.refresh(context.Background()) }()
	}
	dropped := make(chan struct{})
	go func() { group.Wait(); close(dropped) }()
	select {
	case <-dropped:
	case <-time.After(time.Second):
		close(release)
		<-finished
		t.Fatal("refresh calls queued behind network request")
	}
	close(release)
	<-finished
	if calls.Load() != 1 {
		t.Fatal("duplicate HTTP calls")
	}
	if time.Until(s.backoff["account-test"]) < 14*time.Minute {
		t.Fatal("high usage accelerated polling")
	}
}

func TestNewAccountIdentificationDoesNotAlsoPollUsage(t *testing.T) {
	previous := client
	t.Cleanup(func() { client = previous })
	paths := []string{}
	client = &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"account":{"uuid":"account-test","email":"test@example.com"},"organization":{"uuid":"org-test","organization_type":"claude_pro"}}`))}, nil
	})}
	s := pollingFixture(t)
	s.st.Accounts = nil
	s.refresh(context.Background())
	if len(paths) != 1 || paths[0] != "/api/oauth/profile" {
		t.Fatalf("login discovery burst: %v", paths)
	}
	if len(s.st.Accounts) != 1 || s.st.Accounts[0].Email != "test@example.com" {
		t.Fatal("account identity not saved")
	}
	s.refresh(context.Background())
	if len(paths) != 1 {
		t.Fatal("quota was polled immediately after login")
	}
}

func TestScheduleSaveFailurePreventsHTTP(t *testing.T) {
	previous := client
	t.Cleanup(func() { client = previous })
	calls := 0
	client = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	s := pollingFixture(t)
	if err := os.Mkdir(s.statePath(), 0700); err != nil {
		t.Fatal(err)
	}
	s.refresh(context.Background())
	if calls != 0 {
		t.Fatal("HTTP attempted before schedule was durably saved")
	}
}

func TestResponseSaveFailureBlocksRetryAfterRestart(t *testing.T) {
	previous := client
	t.Cleanup(func() { client = previous })
	calls := 0
	s := pollingFixture(t)
	saved := s.statePath() + ".fixture"
	client = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			if err := os.Rename(s.statePath(), saved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(s.statePath(), 0700); err != nil {
				t.Fatal(err)
			}
		}
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	s.refresh(context.Background())
	if err := os.Remove(s.statePath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, s.statePath()); err != nil {
		t.Fatal(err)
	}
	loaded := newSwitcher(s.dir)
	loaded.d = s.d
	loaded.tokenReader = s.tokenReader
	loaded.backoff["account-test"] = time.Now().Add(-time.Minute)
	loaded.refresh(context.Background())
	if calls != 1 {
		t.Fatal("unrecorded response was retried after restart")
	}
}

func TestDelayedPendingLoginStillSyncsOriginalChat(t *testing.T) {
	previous := client
	t.Cleanup(func() { client = previous })
	calls := 0
	client = &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"account":{"uuid":"account-test","email":"test@example.com"},"organization":{"uuid":"org-test"}}`))}, nil
	})}
	s := pollingFixture(t)
	s.st.Accounts = nil
	s.loginPending.Store(true)
	s.addingAt = time.Now().Add(-31 * time.Minute)
	s.quitApp = func() error { return nil }
	s.startApp = func(string) error { return nil }
	write(t, filepath.Join(s.d.dir, "Cookies"), "fixture")
	write(t, filepath.Join(s.d.sessionsDir(), "old-account", "old-org", "local_original.json"), `{"cliSessionId":"original","lastActivityAt":100}`)
	s.refresh(context.Background())
	s.backoff["account-test"] = time.Now().Add(-time.Minute)
	s.refresh(context.Background())
	if _, err := os.Stat(filepath.Join(s.d.sessionsDir(), "account-test", "org-test", "local_original.json")); err != nil {
		t.Fatal("delayed login did not restore original chat entry")
	}
}
