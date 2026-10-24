// Modified 2026-10-19: Simplified Chinese user-facing text and date display.
package main

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"fyne.io/systray"
)

const maxAccounts = 8

var thresholds = []float64{90, 94, 96, 98}

type tray struct {
	s          *switcher
	status     *systray.MenuItem
	update     *systray.MenuItem
	accounts   [maxAccounts]*systray.MenuItem
	auto       *systray.MenuItem
	spend      *systray.MenuItem
	thresholds []*systray.MenuItem
	add        *systray.MenuItem
	importNow  *systray.MenuItem
	autostart  *systray.MenuItem
	lastIcon   string
	updates    chan struct{}
	openWindow func()
	exitApp    func()
	onAdd      func()
	onSwitch   func(string)
	onSync     func()
}

func (t *tray) onReady() {
	systray.SetTooltip("Claude 账号切换器（中文版）")
	t.setIcon(-1, defaultThreshold)
	if t.openWindow != nil {
		open := systray.AddMenuItem("打开账号工作台", "显示账号、额度和 Chrome 绑定")
		go func() {
			for range open.ClickedCh {
				t.openWindow()
			}
		}()
		systray.AddSeparator()
	}

	t.status = systray.AddMenuItem("", "")
	t.status.Disable()
	t.status.Hide()
	t.update = systray.AddMenuItem("", "打开本项目的发布页面")
	t.update.Hide()
	for i := range t.accounts {
		t.accounts[i] = systray.AddMenuItemCheckbox("", "将 Claude 切换到此账号", false)
		t.accounts[i].Hide()
	}
	systray.AddSeparator()
	t.auto = systray.AddMenuItemCheckbox("用量接近上限时自动切号", "切换到剩余额度最多的账号", false)
	limit := systray.AddMenuItem("自动切号阈值（已用）", "")
	for _, v := range thresholds {
		t.thresholds = append(t.thresholds, limit.AddSubMenuItemCheckbox(fmt.Sprintf("%.0f%%", v), "", false))
	}
	t.spend = systray.AddMenuItemCheckbox("周额度重置前自动切号", "切换到一天内重置且至少剩余一半周额度的账号", false)
	t.add = systray.AddMenuItem("添加账号…", "保存当前账号的登录状态，重启 Claude 后登录另一个账号")
	t.importNow = systray.AddMenuItem("同步所有账号的 Code 聊天", "同步本机 Code 聊天列表并重启 Claude，让当前账号也能打开原聊天")
	systray.AddSeparator()
	// Rewriting an enabled entry keeps its path current and adds the Task
	// Manager record that entries from before v0.1.2 are missing.
	if autostartEnabled() {
		if err := setAutostart(true); err != nil {
			log.Printf("refresh start at login: %v", err)
		}
	}
	t.autostart = systray.AddMenuItemCheckbox("开机自动启动", "", autostartEnabled())
	folder := systray.AddMenuItem("打开配置与备份文件夹", "")
	quit := systray.AddMenuItem("退出切换器", "")

	for i, item := range t.accounts {
		go func() {
			for range item.ClickedCh {
				snap := t.s.snapshot()
				if i < len(snap.Accounts) && snap.Accounts[i].UUID != snap.Active {
					if t.onSwitch != nil {
						t.onSwitch(snap.Accounts[i].UUID)
					} else {
						go t.s.run("切换账号", func() error { return t.s.switchTo(snap.Accounts[i].UUID, byHand) })
					}
				}
				t.s.onChange()
			}
		}()
	}
	for i, item := range t.thresholds {
		go func() {
			for range item.ClickedCh {
				t.s.setThreshold(thresholds[i])
			}
		}()
	}
	go func() {
		for {
			select {
			case <-t.auto.ClickedCh:
				t.s.setAuto(!t.auto.Checked())
			case <-t.spend.ClickedCh:
				t.s.setSpendWeekly(!t.spend.Checked())
			case <-t.add.ClickedCh:
				if t.onAdd != nil {
					t.onAdd()
				} else {
					go t.s.run("添加账号", t.s.addAccount)
				}
			case <-t.importNow.ClickedCh:
				if t.onSync != nil {
					t.onSync()
				} else {
					go t.s.run("同步聊天", t.s.importChats)
				}
			case <-t.autostart.ClickedCh:
				if err := setAutostart(!t.autostart.Checked()); err != nil {
					t.s.setStatus("设置开机启动失败：" + err.Error())
				}
				setChecked(t.autostart, autostartEnabled())
			case <-t.update.ClickedCh:
				shellOpen(repoURL + "/releases/latest")
			case <-folder.ClickedCh:
				shellOpen(t.s.dir)
			case <-quit.ClickedCh:
				if t.exitApp != nil {
					t.exitApp()
				} else {
					systray.Quit()
				}
			}
		}
	}()

	// Menu updates arrive from worker goroutines; one goroutine applies them.
	t.s.onChange = func() {
		select {
		case t.updates <- struct{}{}:
		default:
		}
	}
	go func() {
		for range t.updates {
			t.render()
		}
	}()
	t.s.onChange()
	go t.loop()
	go t.updateLoop()
}

// loop runs refresh every few seconds while a new sign-in is expected,
// otherwise every minute; refresh decides which accounts are due.
func (t *tray) loop() {
	var last time.Time
	for ; ; time.Sleep(5 * time.Second) {
		if !t.s.adding() && time.Since(last) < hotEvery {
			continue
		}
		last = time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		t.s.refresh(ctx)
		cancel()
	}
}

// updateLoop looks for a new release a minute after start, then every hour;
// checkUpdate decides whether a day has passed since the last answer.
func (t *tray) updateLoop() {
	time.Sleep(time.Minute)
	for {
		t.s.checkUpdate(context.Background())
		time.Sleep(time.Hour)
	}
}

func (t *tray) render() {
	snap := t.s.snapshot()
	now := time.Now()

	switch {
	case snap.Status != "":
		t.status.SetTitle(snap.Status)
		t.status.Show()
	case snap.Note != "":
		t.status.SetTitle(snap.Note)
		t.status.Show()
	default:
		t.status.Hide()
	}
	if snap.Update != "" {
		t.update.SetTitle("发现更新 " + snap.Update + "，点击查看发布页")
		t.update.Show()
	} else {
		t.update.Hide()
	}
	if len(snap.Accounts) == 0 && snap.Status == "" {
		t.status.SetTitle("请打开 Claude 并登录账号")
		t.status.Show()
	}

	activePct := -1.0
	tip := "Claude 账号切换器（中文版）"
	for i, item := range t.accounts {
		if i >= len(snap.Accounts) {
			item.Hide()
			continue
		}
		a := snap.Accounts[i]
		detail := usageText(a, snap.Threshold, now)
		item.SetTitle(a.label() + menuTab + detail)
		setChecked(item, a.UUID == snap.Active)
		if snap.Busy {
			item.Disable()
		} else {
			item.Enable()
		}
		item.Show()
		if a.UUID == snap.Active {
			tip = a.label() + "\n" + detail
			if a.Usage != nil {
				activePct = a.Usage.current(now).Session
			}
		}
	}
	systray.SetTooltip(tip)
	t.setIcon(activePct, snap.Threshold)

	setChecked(t.auto, snap.AutoSwitch)
	setChecked(t.spend, snap.SpendWeekly)
	for i, item := range t.thresholds {
		setChecked(item, thresholds[i] == snap.Threshold)
	}
	for _, item := range []*systray.MenuItem{t.add, t.importNow} {
		if snap.Busy {
			item.Disable()
		} else {
			item.Enable()
		}
	}
}

// menuTab right-aligns the usage on Windows, where a tab in a menu title
// starts a right-aligned column.
var menuTab = map[bool]string{true: "\t", false: "    "}[runtime.GOOS == "windows"]

func usageText(a Account, threshold float64, now time.Time) string {
	if a.Problem != "" {
		return a.Problem
	}
	if a.Usage == nil {
		return "用量暂不可用"
	}
	u := a.Usage.current(now)
	parts := []string{fmt.Sprintf("5 小时已用 %.0f%%", u.Session), fmt.Sprintf("本周已用 %.0f%%", u.Weekly)}
	if u.Session >= threshold && !u.SessionReset.IsZero() {
		parts[0] += "，重置于 " + resetText(u.SessionReset, now)
	}
	// The weekly reset is always shown: it decides how much of the week is left to use.
	switch {
	case u.WeeklyReset.IsZero() || !u.WeeklyReset.After(now):
	case u.Weekly >= threshold:
		parts[1] += "，重置于 " + resetText(u.WeeklyReset, now)
	default:
		parts[1] += "，重置于 " + resetText(u.WeeklyReset, now)
	}
	return strings.Join(parts, " · ")
}

func resetText(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	weekdays := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	weekday := weekdays[at.Weekday()]
	switch {
	case at.YearDay() == now.YearDay() && at.Year() == now.Year():
		return at.Format("15:04")
	case at.Sub(now) < 6*24*time.Hour:
		return weekday + at.Format(" 15:04")
	}
	// A weekday alone would read as today for a reset a week out.
	return at.Format("1月2日 ") + weekday + at.Format(" 15:04")
}

func (t *tray) setIcon(pct, threshold float64) {
	key := fmt.Sprintf("%.0f/%.0f", pct, threshold)
	if key == t.lastIcon {
		return
	}
	t.lastIcon = key
	systray.SetIcon(trayIcon(pct, threshold))
}

func setChecked(item *systray.MenuItem, on bool) {
	if on != item.Checked() {
		if on {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}
