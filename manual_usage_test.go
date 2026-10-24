package main

import (
	"context"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const manualUsageBody = `{"five_hour":{"utilization":23,"resets_at":"2030-01-02T03:04:05Z"},"seven_day":{"utilization":47,"resets_at":"2030-01-08T03:04:05Z"}}`

func TestManualUsageFetchesMissingCacheWithoutOrdinaryWait(t *testing.T) {
	s := pollingFixture(t)
	s.backoff["account-test"] = time.Now().Add(15 * time.Minute)
	otherNext := time.Now().Add(-time.Hour)
	otherUsage := Usage{Session: 7, Weekly: 11, CheckedAt: time.Now().Add(-time.Hour)}
	otherCached := otherUsage
	s.st.Accounts = append(s.st.Accounts, &Account{UUID: "other", Org: "other-org", Usage: &otherCached})
	s.backoff["other"] = otherNext
	s.tokenReader = func(id, active string) (string, error) {
		if id != "account-test" || active != "account-test" {
			t.Errorf("requested another account's token: %q, %q", id, active)
		}
		return "synthetic-token", nil
	}
	restarts := 0
	s.quitApp = func() error { restarts++; return nil }
	s.startApp = func(string) error { restarts++; return nil }
	paths := []string{}
	identityHTTP(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		return identityResponse(manualUsageBody), nil
	})
	began := time.Now()
	message, err := s.refreshCurrentUsage(context.Background())
	if err != nil || message == "" {
		t.Fatalf("manual usage did not complete: %q, %v", message, err)
	}
	if len(paths) != 1 || paths[0] != "/api/oauth/usage" {
		t.Fatalf("manual usage requested unexpected endpoints: %v", paths)
	}
	u := s.account("account-test").Usage
	if u == nil || u.Session != 23 || u.Weekly != 47 || u.CheckedAt.Before(began) || u.CheckedAt.After(time.Now()) {
		t.Fatalf("current account cache not refreshed: %+v", u)
	}
	if !u.SessionReset.Equal(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)) || !u.WeeklyReset.Equal(time.Date(2030, 1, 8, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("reset times not preserved: %+v", u)
	}
	if restarts != 0 || !s.backoff["other"].Equal(otherNext) || *s.account("other").Usage != otherUsage {
		t.Fatal("manual usage touched the application or another account's schedule/cache")
	}
	loaded := newSwitcher(s.dir)
	if saved := loaded.account("account-test"); saved == nil || saved.Usage == nil || saved.Usage.Session != 23 || saved.Usage.Weekly != 47 {
		t.Fatal("successful usage result was not persisted")
	}
}

func TestManualUsageHonorsRateLimitAndIncompleteCheck(t *testing.T) {
	for _, reason := range []string{"rate limit", "incomplete check", "busy", "shutdown"} {
		t.Run(reason, func(t *testing.T) {
			s := pollingFixture(t)
			switch reason {
			case "rate limit":
				s.st.RateLimitedUntil["account-test"] = time.Now().Add(time.Hour)
			case "incomplete check":
				s.st.CheckPending["account-test"] = true
			case "busy":
				s.busy.Store(true)
			case "shutdown":
				s.shuttingDown.Store(true)
			}
			calls := 0
			identityHTTP(t, func(*http.Request) (*http.Response, error) {
				calls++
				return identityResponse(manualUsageBody), nil
			})
			if _, err := s.refreshCurrentUsage(context.Background()); err == nil || calls != 0 {
				t.Fatalf("manual usage bypassed %s: calls=%d, err=%v", reason, calls, err)
			}
			if s.account("account-test").Usage != nil {
				t.Fatal("blocked request changed the missing cache")
			}
		})
	}
}

func TestManualUsageDropsConcurrentRequests(t *testing.T) {
	s := pollingFixture(t)
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	defer unblock()
	identityHTTP(t, func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return identityResponse(manualUsageBody), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := s.refreshCurrentUsage(ctx); first <- err }()
	select {
	case <-entered:
	case err := <-first:
		t.Fatalf("first usage request returned without reaching HTTP: %v", err)
	case <-ctx.Done():
		t.Fatal("first usage request never reached the HTTP boundary")
	}
	second := make(chan error, 1)
	go func() { _, err := s.refreshCurrentUsage(ctx); second <- err }()
	select {
	case err := <-second:
		if err == nil {
			t.Error("concurrent manual request was accepted")
		}
	case <-time.After(time.Second):
		unblock()
		<-first
		<-second
		t.Fatal("concurrent manual request queued behind the first request")
	}
	s.refresh(ctx)
	unblock()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent refreshes sent %d HTTP requests", calls.Load())
	}
}

func TestManualUsageDuplicateProtectionSurvivesRestart(t *testing.T) {
	s := pollingFixture(t)
	calls := 0
	identityHTTP(t, func(*http.Request) (*http.Response, error) {
		calls++
		return identityResponse(manualUsageBody), nil
	})
	if _, err := s.refreshCurrentUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.refreshCurrentUsage(context.Background()); err == nil || calls != 1 {
		t.Fatal("immediate repeat bypassed the manual usage retry gap")
	}
	loaded := newSwitcher(s.dir)
	loaded.d = s.d
	loaded.tokenReader = s.tokenReader
	if _, err := loaded.refreshCurrentUsage(context.Background()); err == nil || calls != 1 {
		t.Fatal("restarting bypassed the manual usage retry gap")
	}
}

func TestManualUsagePersistenceFailureKeepsOriginalCache(t *testing.T) {
	for _, stage := range []string{"reservation", "response"} {
		t.Run(stage, func(t *testing.T) {
			s := pollingFixture(t)
			before := Usage{Session: 4, Weekly: 9, CheckedAt: time.Now().Add(-time.Hour)}
			cached := before
			s.account("account-test").Usage = &cached
			if err := s.saveState(); err != nil {
				t.Fatal(err)
			}
			blockedPath := s.statePath() + ".switcher-tmp"
			if stage == "reservation" {
				if err := os.Mkdir(blockedPath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			identityHTTP(t, func(*http.Request) (*http.Response, error) {
				calls++
				if err := os.Mkdir(blockedPath, 0700); err != nil {
					t.Fatal(err)
				}
				return identityResponse(manualUsageBody), nil
			})
			if _, err := s.refreshCurrentUsage(context.Background()); err == nil {
				t.Fatal("usage save failure was reported as success")
			}
			if stage == "response" && calls != 1 {
				t.Fatalf("response persistence failure never exercised: HTTP calls=%d", calls)
			}
			if u := s.account("account-test").Usage; u == nil || *u != before {
				t.Fatalf("save failure replaced the previous in-memory cache: %+v", u)
			}
			if stage == "reservation" && calls != 0 {
				t.Fatal("HTTP sent before the reservation was saved")
			}
			if err := os.Remove(blockedPath); err != nil {
				t.Fatal(err)
			}
			loaded := newSwitcher(s.dir)
			loaded.d = s.d
			loaded.tokenReader = s.tokenReader
			u := loaded.account("account-test").Usage
			if u == nil || u.Session != before.Session || u.Weekly != before.Weekly || !u.CheckedAt.Equal(before.CheckedAt) {
				t.Fatalf("save failure replaced the persisted cache: %+v", u)
			}
			if stage == "response" {
				if !loaded.st.CheckPending["account-test"] || !s.st.CheckPending["account-test"] {
					t.Fatal("unsaved response lost its incomplete-check protection")
				}
				if _, err := loaded.refreshCurrentUsage(context.Background()); err == nil || calls != 1 {
					t.Fatal("restarting retried an unsaved usage response")
				}
			}
		})
	}
}

func TestManualUsageRejectsAccountChangeDuringRequest(t *testing.T) {
	s := pollingFixture(t)
	before := Usage{Session: 4, Weekly: 9, CheckedAt: time.Now().Add(-time.Hour)}
	cached := before
	s.account("account-test").Usage = &cached
	otherBefore := Usage{Session: 18, Weekly: 29, CheckedAt: time.Now().Add(-time.Hour)}
	otherCached := otherBefore
	s.st.Accounts = append(s.st.Accounts, &Account{UUID: "other", Org: "other-org", Usage: &otherCached})
	calls := 0
	identityHTTP(t, func(*http.Request) (*http.Response, error) {
		calls++
		write(t, s.d.configPath(), `{"lastKnownAccountUuid":"other","oauth:tokenCacheV2":"synthetic-other"}`)
		return identityResponse(manualUsageBody), nil
	})
	if _, err := s.refreshCurrentUsage(context.Background()); err == nil {
		t.Fatal("account changed during the request but refresh reported success")
	}
	if calls != 1 {
		t.Fatalf("active-account change never exercised: HTTP calls=%d", calls)
	}
	if *s.account("account-test").Usage != before || *s.account("other").Usage != otherBefore {
		t.Fatal("response was attached after the active account changed")
	}
}
