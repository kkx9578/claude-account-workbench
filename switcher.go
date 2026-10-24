// Modified 2026-10-19: Simplified Chinese user-facing text and date display.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Account is a Claude account the switcher has seen signed in.
type Account struct {
	UUID          string `json:"uuid"`
	Org           string `json:"org"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	Plan          string `json:"plan"`
	Usage         *Usage `json:"usage,omitempty"`
	Problem       string `json:"problem,omitempty"`
	NeedsChatSync bool   `json:"needsChatSync,omitempty"`
}

func (a *Account) label() string {
	name := a.Name
	if name == "" {
		name = a.Email
	}
	if a.Plan != "" {
		name += " (" + a.Plan + ")"
	}
	return name
}

// State is what the switcher remembers between runs.
type State struct {
	Accounts   []*Account `json:"accounts"`
	AutoSwitch bool       `json:"autoSwitch"`
	Threshold  float64    `json:"threshold"`
	// SpendWeekly moves to an account whose week is about to reset mostly unused.
	SpendWeekly bool      `json:"spendWeekly"`
	LastSwitch  time.Time `json:"lastSwitch"`
	HandSwitch  time.Time `json:"handSwitch"`
	// Why the last switch happened, and the account it left.
	LastCause switchCause `json:"lastCause,omitempty"`
	LastFrom  string      `json:"lastFrom,omitempty"`
	// Spend weekly moved to this account and keeps coming back to it until
	// its week resets, even once more than half of it is used.
	Spending   string    `json:"spending,omitempty"`
	SpendUntil time.Time `json:"spendUntil,omitempty"`
	AppExe     string    `json:"appExe,omitempty"`
	// The daily check for a new release is on unless this is set by hand.
	NoUpdateCheck    bool                 `json:"noUpdateCheck,omitempty"`
	UpdateChecked    time.Time            `json:"updateChecked,omitempty"`
	LatestVersion    string               `json:"latestVersion,omitempty"`
	UpdateSource     string               `json:"updateSource,omitempty"`
	NextChecks       map[string]time.Time `json:"nextChecks,omitempty"`
	RateStrikes      map[string]int       `json:"rateStrikes,omitempty"`
	CheckPending     map[string]bool      `json:"checkPending,omitempty"`
	RateLimitedUntil map[string]time.Time `json:"rateLimitedUntil"`
	ProfileChecks    map[string]time.Time `json:"profileChecks,omitempty"`
	UsageChecks      map[string]time.Time `json:"usageChecks,omitempty"`
}

// switchCause says why a switch happened.
type switchCause int

const (
	byHand switchCause = iota
	nearlyFull
	spendWeekly
)

const (
	defaultThreshold = 96
	// An auto switch only goes to an account with this much room below the threshold.
	autoHeadroom = 10
	// No second auto switch this soon after the last switch.
	autoCooldown = 10 * time.Minute
	// Spend weekly: an account whose week resets this soon with at most this
	// much of it used is worth moving to before the rest is lost.
	expiringWithin    = 24 * time.Hour
	expiringMaxWeekly = 50
	// A switch made by hand pauses spend weekly this long.
	handPause = 5 * time.Hour
	// Spend weekly is never urgent, so it waits until no chat has been
	// active this long instead of cutting off a reply.
	idleFor = 15 * time.Minute
	// The menu says why the last auto switch happened for this long.
	noteFor = 12 * time.Hour

	// Claude also polls usage. Keep our checks sparse, including near a limit.
	activeEvery = 15 * time.Minute
	hotEvery    = time.Minute
	idleEvery   = 30 * time.Minute
	// Auto switch only trusts numbers this fresh.
	activeFresh = 5 * time.Minute
	idleFresh   = idleEvery + 5*time.Minute
	maxBackoff  = 2 * time.Hour
)

type switcher struct {
	d            desktop
	dir          string
	mu           sync.Mutex
	st           State
	active       string
	busy         atomic.Bool
	loginPending atomic.Bool
	checking     atomic.Bool
	shuttingDown atomic.Bool
	status       string
	addingAt     time.Time
	onChange     func()
	// These maps alias persisted scheduling state, including successful checks.
	backoff      map[string]time.Time
	strikes      map[string]int
	refreshMu    sync.Mutex
	phase        string
	refreshError string
	quitApp      func() error
	startApp     func(string) error
	tokenReader  func(string, string) (string, error)
}

func newSwitcher(dir string) *switcher {
	s := &switcher{d: claudeDesktop(), dir: dir, st: State{Threshold: defaultThreshold}, onChange: func() {},
		backoff: map[string]time.Time{}, strikes: map[string]int{}, quitApp: quitDesktop, startApp: startDesktop}
	if data, err := os.ReadFile(s.statePath()); err == nil {
		_ = json.Unmarshal(data, &s.st)
	}
	if s.st.UpdateSource != repoURL {
		s.st.LatestVersion = ""
		s.st.UpdateChecked = time.Time{}
		s.st.UpdateSource = repoURL
	}
	if s.st.Threshold <= 0 {
		s.st.Threshold = defaultThreshold
	}
	if s.st.NextChecks == nil {
		s.st.NextChecks = map[string]time.Time{}
	}
	if s.st.RateStrikes == nil {
		s.st.RateStrikes = map[string]int{}
	}
	if s.st.CheckPending == nil {
		s.st.CheckPending = map[string]bool{}
	}
	if s.st.RateLimitedUntil == nil {
		s.st.RateLimitedUntil = map[string]time.Time{}
		for id, count := range s.st.RateStrikes {
			if count > 0 {
				s.st.RateLimitedUntil[id] = s.st.NextChecks[id]
			}
		}
	}
	if s.st.ProfileChecks == nil {
		s.st.ProfileChecks = map[string]time.Time{}
	}
	if s.st.UsageChecks == nil {
		s.st.UsageChecks = map[string]time.Time{}
	}
	s.backoff, s.strikes = s.st.NextChecks, s.st.RateStrikes
	// An older build did not persist cooldowns. Do not issue another request
	// immediately after upgrading or reopening an old state file.
	for _, a := range s.st.Accounts {
		if _, exists := s.backoff[a.UUID]; !exists {
			s.backoff[a.UUID] = time.Now().Add(activeEvery)
		}
	}
	s.tokenReader = s.tokenFor
	return s
}

func (s *switcher) statePath() string             { return filepath.Join(s.dir, "state.json") }
func (s *switcher) profileDir(uuid string) string { return filepath.Join(s.dir, "accounts", uuid) }
func (s *switcher) backupsDir() string            { return filepath.Join(s.dir, "backups") }

// saveState writes state.json. Callers hold s.mu.
func (s *switcher) saveState() error {
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.statePath(), data); err != nil {
		log.Printf("save state: %v", err)
		return err
	}
	return nil
}

func (s *switcher) setStatus(msg string) {
	s.mu.Lock()
	s.status = msg
	s.mu.Unlock()
	if msg != "" {
		log.Print(msg)
	}
	s.onChange()
}

func (s *switcher) setPhase(phase, msg string) {
	s.mu.Lock()
	s.phase = phase
	s.status = msg
	s.mu.Unlock()
	s.onChange()
}

func (s *switcher) account(uuid string) *Account {
	for _, a := range s.st.Accounts {
		if a.UUID == uuid {
			return a
		}
	}
	return nil
}

// tokenFor returns an access token for an account: from the app's live config
// when it is signed in, otherwise from its saved profile.
func (s *switcher) tokenFor(uuid, active string) (string, error) {
	path := filepath.Join(s.profileDir(uuid), "config.json")
	if uuid == active {
		path = s.d.configPath()
	}
	cache, err := tokenCacheOf(path)
	if err != nil {
		return "", err
	}
	return accessToken(cache, uuid)
}

// refresh notices sign-ins, keeps usage current and runs the auto switch.
func (s *switcher) refresh(ctx context.Context) {
	if s.busy.Load() || s.shuttingDown.Load() {
		return
	}
	if !s.refreshMu.TryLock() {
		return
	}
	if s.busy.Load() {
		s.refreshMu.Unlock()
		return
	}
	s.checking.Store(true)
	active, err := s.d.activeAccount()
	if err != nil {
		log.Printf("read app config: %v", err)
	}
	_, exe := desktopProcesses()

	s.mu.Lock()
	s.active = active
	if exe != "" {
		s.st.AppExe = exe
	}
	type job struct {
		uuid string
		new  bool
	}
	now := time.Now()
	var jobs []job
	if active != "" && s.account(active) == nil && !s.st.CheckPending[active] && now.After(s.backoff[active]) {
		jobs = append(jobs, job{active, true})
	}
	for _, a := range s.st.Accounts {
		every := idleEvery
		if a.UUID == active {
			every = activeEvery
		}
		due := a.Usage == nil || now.Sub(a.Usage.CheckedAt) >= every-5*time.Second
		if due && !s.st.CheckPending[a.UUID] && now.After(s.backoff[a.UUID]) {
			jobs = append(jobs, job{a.UUID, false})
		}
	}
	s.mu.Unlock()

	added := false
	cycleError := ""
	for _, j := range jobs {
		if s.shuttingDown.Load() {
			break
		}
		every := idleEvery
		if j.uuid == active {
			every = activeEvery
		}
		token, err := s.tokenReader(j.uuid, active)
		if err != nil {
			s.mu.Lock()
			if a := s.account(j.uuid); a != nil && (errors.Is(err, errSignedOut) || errors.Is(err, errAuthNotReady)) {
				a.Problem = err.Error()
			}
			s.mu.Unlock()
			if cycleError == "" {
				cycleError = "授权检查：" + err.Error()
			}
			continue
		}
		// Reserve and persist the next attempt before touching the network.
		s.mu.Lock()
		previous := s.backoff[j.uuid]
		previousUsage := s.st.UsageChecks[j.uuid]
		s.backoff[j.uuid] = time.Now().Add(every)
		if !j.new {
			s.st.UsageChecks[j.uuid] = time.Now()
		}
		s.st.CheckPending[j.uuid] = true
		saveErr := s.saveState()
		if saveErr != nil {
			delete(s.st.CheckPending, j.uuid)
			s.backoff[j.uuid] = previous
			s.st.UsageChecks[j.uuid] = previousUsage
		}
		s.mu.Unlock()
		if saveErr != nil {
			cycleError = "无法保存额度检查计划，已停止请求。请检查配置目录的写入权限。"
			break
		}
		var p profile
		var u *Usage
		if err == nil && j.new {
			p, err = fetchProfile(ctx, token)
		}
		if err == nil && !j.new {
			u, err = fetchUsage(ctx, token)
		}

		s.mu.Lock()
		var limited *rateLimited
		if errors.As(err, &limited) {
			s.strikes[j.uuid]++
			wait := max(min((30*time.Minute)<<min(s.strikes[j.uuid]-1, 2), maxBackoff), limited.after)
			s.backoff[j.uuid] = time.Now().Add(wait)
			s.st.RateLimitedUntil[j.uuid] = s.backoff[j.uuid]
			log.Printf("quota rate limited; next attempt at %s", s.backoff[j.uuid].Format(time.RFC3339))
		} else if err == nil {
			delete(s.strikes, j.uuid)
		}
		if limited == nil {
			delete(s.st.RateLimitedUntil, j.uuid)
		}
		a := s.account(j.uuid)
		if j.new && err == nil {
			a = &Account{UUID: j.uuid, Org: p.Organization.UUID, Name: p.Account.DisplayName, Email: p.Account.Email, Plan: p.plan()}
			s.st.Accounts = append(s.st.Accounts, a)
			added = true
			log.Printf("added account %s", a.label())
		}
		if a != nil {
			switch {
			case err == nil:
				a.Usage, a.Problem = u, ""
			case errors.Is(err, errSignedOut):
				a.Problem = "登录已失效，请重新添加账号"
			case limited != nil:
				// Logged above; the last numbers stay until the next try.
			default:
				log.Printf("usage %s: %v", a.label(), err)
			}
		}
		if err != nil && cycleError == "" {
			if limited == nil {
				cycleError = "额度暂未更新：" + err.Error()
			}
		}
		delete(s.st.CheckPending, j.uuid)
		if err := s.saveState(); err != nil {
			s.st.CheckPending[j.uuid] = true
			cycleError = "检查结果未能保存，额度检查已暂停。请保留配置文件以便排查。"
		}
		s.mu.Unlock()
	}

	lastChat := lastChatWrite(s.d.sessionsDir())
	s.mu.Lock()
	if len(jobs) > 0 {
		s.refreshError = cycleError
	}
	adding := !s.addingAt.IsZero() || s.loginPending.Load()
	if added && adding {
		s.addingAt = time.Time{}
	} else if adding && !s.loginPending.Load() && time.Since(s.addingAt) > 15*time.Minute {
		s.addingAt, s.status = time.Time{}, ""
	}
	s.saveState()
	target, cause := s.autoTarget(desktopRunning(), lastChat, time.Now())
	s.mu.Unlock()
	s.checking.Store(false)
	s.onChange()
	s.refreshMu.Unlock()

	switch {
	case added && adding:
		// A fresh sign-in: restart once so the chats of the other accounts show up.
		s.run("同步聊天", func() error { return s.importChats() })
	case target != nil:
		s.run("自动切号", func() error { return s.switchTo(target.UUID, cause) })
	}
}

// autoTarget picks the account to switch to and says why: the active one is
// nearly full, or another one's week is about to reset mostly unused.
// lastChat is when a chat was last active. Callers hold s.mu.
func (s *switcher) autoTarget(running bool, lastChat, now time.Time) (*Account, switchCause) {
	if s.loginPending.Load() {
		return nil, byHand
	}
	cur := s.account(s.active)
	if !running || cur == nil || cur.Usage == nil || now.Sub(cur.Usage.CheckedAt) > activeFresh || now.Sub(s.st.LastSwitch) < autoCooldown {
		return nil, byHand
	}
	if s.st.AutoSwitch && cur.Usage.current(now).peak() >= s.st.Threshold {
		var best *Account
		for _, a := range s.st.Accounts {
			if s.usable(a, cur, now) && (best == nil || a.Usage.current(now).peak() < best.Usage.current(now).peak()) {
				best = a
			}
		}
		if best != nil {
			return best, nearlyFull
		}
	}
	// The active account keeps going while its own week is the one running out.
	if s.st.SpendWeekly && now.Sub(s.st.HandSwitch) >= handPause && now.Sub(lastChat) >= idleFor && !s.spendable(cur, now) {
		var best *Account
		for _, a := range s.st.Accounts {
			if s.usable(a, cur, now) && s.spendable(a, now) && (best == nil || a.Usage.WeeklyReset.Before(best.Usage.WeeklyReset)) {
				best = a
			}
		}
		if best != nil {
			return best, spendWeekly
		}
	}
	return nil, byHand
}

// usable says whether an auto switch may go to a: fresh numbers and room
// well below the threshold. Callers hold s.mu.
func (s *switcher) usable(a, cur *Account, now time.Time) bool {
	return a != cur && a.Usage != nil && a.Problem == "" && now.Sub(a.Usage.CheckedAt) <= idleFresh &&
		a.Usage.current(now).peak() < s.st.Threshold-autoHeadroom
}

// spendable says whether a's week should be used up first: it resets soon
// with much of it unused, or spend weekly already moved there and that week
// has not reset yet. a has usage. Callers hold s.mu.
func (s *switcher) spendable(a *Account, now time.Time) bool {
	if a.UUID == s.st.Spending && now.Before(s.st.SpendUntil) {
		return true
	}
	u := a.Usage.current(now)
	left := u.WeeklyReset.Sub(now)
	return !u.WeeklyReset.IsZero() && left > 0 && left <= expiringWithin && u.Weekly <= expiringMaxWeekly
}

// run does one app restart at a time and reports the outcome in the menu.
func (s *switcher) run(what string, f func() error) {
	if !s.busy.CompareAndSwap(false, true) {
		return
	}
	s.runReserved(what, f)
}

func (s *switcher) runReserved(what string, f func() error) {
	s.refreshMu.Lock()
	err := f()
	s.refreshMu.Unlock()
	s.busy.Store(false)
	if err != nil {
		s.setPhase("error", fmt.Sprintf("%s失败：%v", what, err))
		return
	}
	s.mu.Lock()
	if s.addingAt.IsZero() {
		s.status = what + "已完成；请在 Claude 中确认当前账号"
		s.phase = "done"
	} else {
		s.phase = "login"
		s.status = "请在官方 Claude 和所选 Chrome 资料中完成登录，再返回核对邮箱"
	}
	s.mu.Unlock()
	s.onChange()
	s.refresh(context.Background())
}

// restart closes the app, runs change on its files and starts it again. If
// change fails, the app files are put back as they were.
func (s *switcher) restart(change func(active string) error) (resultErr error) {
	s.setPhase("closing", "正在退出 Claude，请等待当前窗口关闭")
	if err := s.quitApp(); err != nil {
		return err
	}
	s.mu.Lock()
	exe := s.st.AppExe
	s.mu.Unlock()
	defer func() {
		s.setPhase("starting", "正在重新打开 Claude")
		if err := s.startApp(exe); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("无法启动 Claude：%w", err))
		}
	}()
	s.setPhase("backup", "正在备份当前登录状态")
	bk, err := backup(s.d, s.backupsDir())
	if err != nil {
		return fmt.Errorf("备份失败：%w", err)
	}
	active, err := s.d.activeAccount()
	if err == nil {
		err = change(active)
	}
	if err != nil {
		if rerr := restoreBackup(s.d, bk); rerr != nil {
			log.Printf("restore backup %s: %v", bk, rerr)
		}
	}
	return err
}

// saveActive keeps the signed-in account's latest login before it is replaced.
func (s *switcher) saveActive(active string) error {
	if active == "" {
		return nil
	}
	return saveProfile(s.d, s.profileDir(active))
}

// switchTo signs the app in as uuid, for the reason in cause.
func (s *switcher) switchTo(uuid string, cause switchCause) error {
	if cause != byHand && s.loginPending.Load() {
		return errors.New("登录核对尚未完成，自动切换已暂停")
	}
	s.mu.Lock()
	target := s.account(uuid)
	from := s.active
	s.mu.Unlock()
	if target == nil {
		return errors.New("未找到此账号")
	}
	switch cause {
	case nearlyFull:
		log.Printf("auto switch to %s: the active account is nearly full", target.label())
	case spendWeekly:
		log.Printf("auto switch to %s: its weekly usage resets soon", target.label())
	}
	if _, err := os.Stat(filepath.Join(s.profileDir(uuid), "config.json")); err != nil && uuid != s.snapshot().Active {
		return fmt.Errorf("%s 没有保存的登录状态，请使用“添加账号”", target.label())
	}
	s.setStatus("正在切换到 " + target.label() + "…")
	err := s.restart(func(active string) error {
		if active == uuid {
			return nil
		}
		if err := s.saveActive(active); err != nil {
			return fmt.Errorf("保存当前登录状态失败：%w", err)
		}
		s.setPhase("restoring", "正在恢复目标账号的登录状态")
		if err := restoreProfile(s.d, s.profileDir(uuid)); err != nil {
			return fmt.Errorf("恢复登录状态失败：%w", err)
		}
		s.setPhase("syncing", "正在同步原 Code 聊天入口和项目分组")
		n, err := syncSessions(s.d.sessionsDir(), target.UUID, target.Org)
		log.Printf("switched to %s, %d chats imported", target.label(), n)
		s.importProjects(target)
		return err
	})
	if err == nil {
		actual, checkErr := s.d.activeAccount()
		if checkErr != nil {
			err = checkErr
		} else if actual != uuid {
			err = errors.New("切换后的本地账号与目标不一致，请核对 Claude 登录状态")
		}
	}
	if err == nil {
		s.mu.Lock()
		s.st.LastSwitch = time.Now()
		s.st.LastCause, s.st.LastFrom = cause, from
		switch cause {
		case byHand:
			// The user's pick ends spending another account's week.
			s.st.HandSwitch = s.st.LastSwitch
			s.st.Spending, s.st.SpendUntil = "", time.Time{}
		case spendWeekly:
			if target.Usage != nil {
				s.st.Spending, s.st.SpendUntil = uuid, target.Usage.WeeklyReset
			}
		}
		s.active = uuid
		target.NeedsChatSync = false
		s.saveState()
		s.mu.Unlock()
	}
	return err
}

// addAccount keeps the current sign-in and opens the app on its login screen.
func (s *switcher) addAccount() error {
	s.setStatus("请在 Claude 窗口中登录另一个账号")
	err := s.restart(func(active string) error {
		if err := s.saveActive(active); err != nil {
			return err
		}
		return clearSignIn(s.d)
	})
	if err == nil {
		s.mu.Lock()
		s.addingAt = time.Now()
		s.mu.Unlock()
	}
	return err
}

// importChats makes the chats of all accounts visible in the signed-in one.
func (s *switcher) importChats() error {
	s.setStatus("正在同步 Code 聊天…")
	return s.restart(func(active string) error {
		s.mu.Lock()
		a := s.account(active)
		s.mu.Unlock()
		if a == nil {
			return errors.New("尚未检测到已登录的账号")
		}
		n, err := syncSessions(s.d.sessionsDir(), a.UUID, a.Org)
		log.Printf("%d chats imported into %s", n, a.label())
		s.importProjects(a)
		return err
	})
}

// importProjects copies the projects of all accounts into a. A failure only
// costs the project grouping, so it is logged and the switch goes on.
func (s *switcher) importProjects(a *Account) {
	n, err := syncSpaces(s.d.spacesDir(), a.UUID, a.Org, filepath.Join(s.dir, "spaces-seen.json"))
	if err != nil {
		log.Printf("projects not imported into %s: %v", a.label(), err)
		return
	}
	log.Printf("%d projects imported into %s", n, a.label())
}

func (s *switcher) setAuto(on bool) {
	s.mu.Lock()
	s.st.AutoSwitch = on
	s.saveState()
	s.mu.Unlock()
	s.onChange()
}

func (s *switcher) setSpendWeekly(on bool) {
	s.mu.Lock()
	s.st.SpendWeekly = on
	s.saveState()
	s.mu.Unlock()
	s.onChange()
}

func (s *switcher) setThreshold(t float64) {
	s.mu.Lock()
	s.st.Threshold = t
	s.saveState()
	s.mu.Unlock()
	s.onChange()
}

// snapshot is a copy of what the menu shows.
type snapshot struct {
	Accounts         []Account
	Active           string
	AutoSwitch       bool
	SpendWeekly      bool
	Threshold        float64
	Status           string
	Note             string
	Busy             bool
	Phase            string
	RefreshError     string
	NextChecks       map[string]time.Time
	RateStrikes      map[string]int
	RateLimitedUntil map[string]time.Time
	ProfileChecks    map[string]time.Time
	UsageChecks      map[string]time.Time
	CheckPending     map[string]bool
	Checking         bool
	// Update is a version newer than this one, empty while there is none.
	Update string
}

func (s *switcher) snapshot() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := snapshot{Active: s.active, AutoSwitch: s.st.AutoSwitch, SpendWeekly: s.st.SpendWeekly, Threshold: s.st.Threshold,
		Status: s.status, Note: s.autoNote(time.Now()), Busy: s.busy.Load(), Phase: s.phase, RefreshError: s.refreshError}
	snap.NextChecks = map[string]time.Time{}
	snap.RateStrikes = map[string]int{}
	snap.RateLimitedUntil = map[string]time.Time{}
	snap.ProfileChecks = map[string]time.Time{}
	snap.UsageChecks = map[string]time.Time{}
	for id, at := range s.st.UsageChecks {
		snap.UsageChecks[id] = at
	}
	for id, at := range s.st.RateLimitedUntil {
		snap.RateLimitedUntil[id] = at
	}
	for id, at := range s.st.ProfileChecks {
		snap.ProfileChecks[id] = at
	}
	snap.CheckPending = map[string]bool{}
	snap.Checking = s.checking.Load()
	for id, pending := range s.st.CheckPending {
		snap.CheckPending[id] = pending
	}
	for id, at := range s.backoff {
		snap.NextChecks[id] = at
	}
	for id, count := range s.strikes {
		snap.RateStrikes[id] = count
	}
	if !s.st.NoUpdateCheck && newerVersion(s.st.LatestVersion, version) {
		snap.Update = s.st.LatestVersion
	}
	for _, a := range s.st.Accounts {
		snap.Accounts = append(snap.Accounts, *a)
	}
	return snap
}

// autoNote says why the last switch happened when the switcher made it on
// its own, for a while afterwards. Callers hold s.mu.
func (s *switcher) autoNote(now time.Time) string {
	if now.Sub(s.st.LastSwitch) > noteFor {
		return ""
	}
	at := resetText(s.st.LastSwitch, now)
	switch s.st.LastCause {
	case nearlyFull:
		from := "上一个账号"
		if a := s.account(s.st.LastFrom); a != nil {
			from = a.label()
		}
		return fmt.Sprintf("已于 %s 自动切号，原因：%s 用量接近上限", at, from)
	case spendWeekly:
		return fmt.Sprintf("已于 %s 自动切号，优先使用此账号将在 %s 重置的周额度", at, resetText(s.st.SpendUntil, now))
	}
	return ""
}

func (s *switcher) adding() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.addingAt.IsZero()
}
