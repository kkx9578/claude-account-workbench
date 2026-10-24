package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalAuthorizationNotReadyDoesNotDelayDiscovery(t *testing.T) {
	s := pollingFixture(t)
	s.st.Accounts = nil
	s.tokenReader = func(string, string) (string, error) { return "", errors.New("local authorization not ready") }
	s.refresh(context.Background())
	if time.Until(s.backoff["account-test"]) > 0 {
		t.Fatal("local token failure created a network cooldown")
	}
	previous := client
	t.Cleanup(func() { client = previous })
	calls := 0
	client = &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"account":{"uuid":"account-test","email":"test@example.com"},"organization":{"uuid":"org-test","organization_type":"claude_pro"}}`))}, nil
	})}
	s.tokenReader = func(string, string) (string, error) { return "synthetic", nil }
	s.refresh(context.Background())
	if calls != 1 || len(s.st.Accounts) != 1 {
		t.Fatal("new authorization was not discovered immediately")
	}
}

const identityBody = `{"account":{"uuid":"account-test","email":"test@example.com","display_name":"Test"},"organization":{"uuid":"org-test","organization_type":"claude_pro"}}`

func identityHTTP(t *testing.T, f quotaTransport) {
	t.Helper()
	previous := client
	t.Cleanup(func() { client = previous })
	client = &http.Client{Transport: f}
}
func identityResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}

func TestMissingCodeAuthorizationIsNotExpired(t *testing.T) {
	for _, body := range []string{`{}`, `{"acct:other|client:user:profile":{"token":"synthetic","expiresAt":9999999999999}}`} {
		if _, err := accessTokenFromJSON([]byte(body), "account-test"); !errors.Is(err, errAuthNotReady) {
			t.Fatalf("missing authorization misclassified: %v", err)
		}
	}
	if _, err := accessTokenFromJSON([]byte(`{"acct:account-test|client:user:profile":{"token":"synthetic","expiresAt":1}}`), "account-test"); !errors.Is(err, errSignedOut) {
		t.Fatal("expired token not distinguished")
	}
	body := fmt.Sprintf(`{"acct:account-test|client:user:profile":{"token":"synthetic","expiresAt":%d}}`, time.Now().Add(time.Hour).UnixMilli())
	if _, err := accessTokenFromJSON([]byte(body), "account-test"); err != nil {
		t.Fatal(err)
	}
}

func TestManualIdentityBypassesOrdinaryWaitWithoutUsageOrRestart(t *testing.T) {
	s := pollingFixture(t)
	s.st.Accounts = []*Account{{UUID: "other", Org: "other-org"}}
	until := time.Now().Add(15 * time.Minute)
	s.backoff["account-test"] = until
	s.backoff["other"] = until
	restarts := 0
	s.quitApp = func() error { restarts++; return nil }
	s.startApp = func(string) error { restarts++; return nil }
	paths := []string{}
	identityHTTP(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		return identityResponse(identityBody), nil
	})
	email, err := s.identifyCurrent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if email != "test@example.com" || len(paths) != 1 || paths[0] != "/api/oauth/profile" {
		t.Fatal("manual identity did not use only profile endpoint")
	}
	if restarts != 0 || !s.backoff["other"].Equal(until) {
		t.Fatal("manual identity touched application or other account schedule")
	}
	if !s.backoff["account-test"].Equal(until) {
		t.Fatal("identity refresh postponed the usage schedule")
	}
	loaded := newSwitcher(s.dir)
	if a := loaded.account("account-test"); a == nil || !a.NeedsChatSync {
		t.Fatal("new account lost deferred chat synchronization")
	}
	if _, err = s.identifyCurrent(context.Background()); err == nil || len(paths) != 1 {
		t.Fatal("duplicate manual request not throttled")
	}
}

func TestManualIdentityHonorsRealCooldownAndIncompleteCheck(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			s := pollingFixture(t)
			s.st.CheckPending["account-test"] = blocked
			if !blocked {
				s.st.RateLimitedUntil["account-test"] = time.Now().Add(time.Hour)
			}
			calls := 0
			identityHTTP(t, func(*http.Request) (*http.Response, error) { calls++; return identityResponse(identityBody), nil })
			if _, err := s.identifyCurrent(context.Background()); err == nil || calls != 0 {
				t.Fatal("manual request bypassed protection")
			}
		})
	}
}

func TestManualIdentityRefusesConcurrentChecks(t *testing.T) {
	s := pollingFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	calls := 0
	identityHTTP(t, func(*http.Request) (*http.Response, error) {
		calls++
		close(entered)
		<-release
		return identityResponse(identityBody), nil
	})
	go func() { _, err := s.identifyCurrent(context.Background()); done <- err }()
	<-entered
	if _, err := s.identifyCurrent(context.Background()); err == nil {
		t.Error("concurrent manual request accepted")
	}
	s.refresh(context.Background())
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("manual refresh caused duplicate HTTP calls")
	}
}

func TestManualIdentityRejectsChangedOrWrongAccount(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			s := pollingFixture(t)
			s.st.Accounts = nil
			identityHTTP(t, func(*http.Request) (*http.Response, error) {
				body := identityBody
				if changed {
					write(t, s.d.configPath(), `{"lastKnownAccountUuid":"different","oauth:tokenCacheV2":"synthetic"}`)
				} else {
					body = strings.ReplaceAll(body, "account-test", "different")
				}
				return identityResponse(body), nil
			})
			if _, err := s.identifyCurrent(context.Background()); err == nil || len(s.st.Accounts) != 0 {
				t.Fatal("wrong identity saved")
			}
		})
	}
}

func TestManualIdentityPersistenceFailuresRemainProtected(t *testing.T) {
	for _, afterRequest := range []bool{false, true} {
		t.Run(fmt.Sprint(afterRequest), func(t *testing.T) {
			s := pollingFixture(t)
			s.st.Accounts = nil
			calls := 0
			saved := s.statePath() + ".saved"
			if !afterRequest {
				if err := os.Mkdir(s.statePath(), 0700); err != nil {
					t.Fatal(err)
				}
			}
			identityHTTP(t, func(*http.Request) (*http.Response, error) {
				calls++
				if err := os.Rename(s.statePath(), saved); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(s.statePath(), 0700); err != nil {
					t.Fatal(err)
				}
				return identityResponse(identityBody), nil
			})
			if _, err := s.identifyCurrent(context.Background()); err == nil || len(s.st.Accounts) != 0 {
				t.Fatal("failed save reported success")
			}
			if !afterRequest {
				if calls != 0 {
					t.Fatal("request before persistence")
				}
				return
			}
			if err := os.Remove(s.statePath()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(saved, s.statePath()); err != nil {
				t.Fatal(err)
			}
			loaded := newSwitcher(s.dir)
			loaded.d = s.d
			loaded.tokenReader = s.tokenReader
			if _, err := loaded.identifyCurrent(context.Background()); err == nil || calls != 1 {
				t.Fatal("restart bypassed incomplete result protection")
			}
		})
	}
}

func TestManualDiscoverySyncsChatOnlyAfterExplicitConfirmation(t *testing.T) {
	s := pollingFixture(t)
	s.st.Accounts = nil
	identityHTTP(t, func(*http.Request) (*http.Response, error) { return identityResponse(identityBody), nil })
	stops := 0
	starts := 0
	s.quitApp = func() error { stops++; return nil }
	s.startApp = func(string) error { starts++; return nil }
	write(t, filepath.Join(s.d.dir, "Cookies"), "fixture")
	entry := `{"cliSessionId":"original","lastActivityAt":100}`
	write(t, filepath.Join(s.d.sessionsDir(), "old", "org-old", "local_original.json"), entry)
	if _, err := s.identifyCurrent(context.Background()); err != nil {
		t.Fatal(err)
	}
	u, err := newUI(s, chromeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	u.settings.Pending = &pendingLogin{ProfileID: "Default", ExpectedEmail: "test@example.com"}
	s.loginPending.Store(true)
	if err = u.CompleteLogin(false); err == nil || stops != 0 || starts != 0 {
		t.Fatal("sync ran without confirmation")
	}
	if err = u.CompleteLogin(true); err != nil {
		t.Fatal(err)
	}
	if stops != 1 || starts != 1 || u.settings.Pending != nil || s.account("account-test").NeedsChatSync {
		t.Fatal("confirmed sync did not complete")
	}
	if read(t, filepath.Join(s.d.sessionsDir(), "account-test", "org-test", "local_original.json")) != entry {
		t.Fatal("original conversation ID changed")
	}
}

func TestLocalExpiredCredentialIsExcludedFromAutomaticSwitch(t *testing.T) {
	s := pollingFixture(t)
	now := time.Now()
	s.st.Accounts[0].Usage = &Usage{Session: 99, CheckedAt: now}
	s.st.Accounts = append(s.st.Accounts, &Account{UUID: "expired", Usage: &Usage{Session: 1, CheckedAt: now.Add(-31 * time.Minute)}})
	s.tokenReader = func(string, string) (string, error) { return "", errSignedOut }
	s.refresh(context.Background())
	if s.account("expired").Problem == "" {
		t.Fatal("expired credential has no problem marker")
	}
	s.st.AutoSwitch = true
	if target, _ := s.autoTarget(true, time.Time{}, now); target != nil {
		t.Fatal("expired credential selected for automatic switching")
	}
}
