// Manual account discovery is independent of periodic usage polling.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const identityMinGap = 15 * time.Second

func (s *switcher) refreshCurrentUsage(ctx context.Context) (string, error) {
	return s.refreshCurrent(ctx, true)
}

func (s *switcher) identifyCurrent(ctx context.Context) (string, error) {
	return s.refreshCurrent(ctx, false)
}

// Both manual actions share persistence and throttling guards, but only usage
// requests update the periodic usage schedule.
func (s *switcher) refreshCurrent(ctx context.Context, usageOnly bool) (message string, resultErr error) {
	if s.busy.Load() || s.shuttingDown.Load() {
		return "", errors.New("当前操作尚未完成，请稍后识别")
	}
	if !s.refreshMu.TryLock() {
		return "", errors.New("已有检查正在进行，请稍等")
	}
	if s.busy.Load() || s.shuttingDown.Load() {
		s.refreshMu.Unlock()
		return "", errors.New("当前操作尚未完成，请稍后识别")
	}
	s.checking.Store(true)
	defer func() {
		s.mu.Lock()
		if resultErr != nil {
			label := "账号识别："
			if usageOnly {
				label = "额度刷新："
			}
			s.refreshError = label + resultErr.Error()
		} else {
			s.refreshError = ""
		}
		s.mu.Unlock()
		s.checking.Store(false)
		s.refreshMu.Unlock()
		s.onChange()
	}()
	active, err := s.d.activeAccount()
	if err != nil {
		return "", err
	}
	if active == "" {
		return "", errors.New("请先在桌面 Claude 登录，并打开 Code 页面")
	}
	s.mu.Lock()
	blocked := s.st.CheckPending[active]
	limitedUntil := s.st.RateLimitedUntil[active]
	checks := s.st.ProfileChecks
	if usageOnly {
		checks = s.st.UsageChecks
	}
	nextManual := checks[active].Add(identityMinGap)
	known := s.account(active) != nil
	s.mu.Unlock()
	if usageOnly && !known {
		return "", errors.New("请先重新识别当前账号，再刷新额度")
	}
	if blocked {
		return "", errors.New("上次检查结果未能保存，已暂停请求；请保留配置文件以便排查")
	}
	if time.Now().Before(limitedUntil) {
		return "", fmt.Errorf("服务器限流，请在 %s 后重试", limitedUntil.Local().Format("15:04:05"))
	}
	if time.Now().Before(nextManual) {
		return "", errors.New("刚刚已检查，请等待 15 秒再试")
	}
	token, err := s.tokenReader(active, active)
	if err != nil {
		return "", err
	} // Local readiness never creates a network cooldown.
	s.mu.Lock()
	previousNext, previousCheck := s.backoff[active], checks[active]
	s.st.CheckPending[active] = true
	checks[active] = time.Now()
	if usageOnly {
		s.backoff[active] = time.Now().Add(activeEvery)
	}
	err = s.saveState()
	if err != nil {
		delete(s.st.CheckPending, active)
		s.backoff[active] = previousNext
		checks[active] = previousCheck
	}
	s.mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("无法保存检查计划，未发送请求：%w", err)
	}

	var p profile
	var usage *Usage
	var requestErr error
	if usageOnly {
		usage, requestErr = fetchUsage(ctx, token)
	} else {
		p, requestErr = fetchProfile(ctx, token)
	}
	if requestErr == nil {
		if !usageOnly && (p.Account.UUID != active || strings.TrimSpace(p.Account.Email) == "" || p.Organization.UUID == "") {
			requestErr = errors.New("返回的账号身份不完整或与当前账号不一致，未保存")
		}
		actual, readErr := s.d.activeAccount()
		if readErr != nil {
			requestErr = readErr
		} else if actual != active {
			requestErr = errors.New("检查期间桌面账号发生变化，请重新识别")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var limited *rateLimited
	if errors.As(requestErr, &limited) {
		s.strikes[active]++
		wait := max(min((30*time.Minute)<<min(s.strikes[active]-1, 2), maxBackoff), limited.after)
		s.backoff[active] = time.Now().Add(wait)
		s.st.RateLimitedUntil[active] = s.backoff[active]
	} else {
		delete(s.st.RateLimitedUntil, active)
		if requestErr == nil {
			delete(s.strikes, active)
		}
	}
	var previousAccount Account
	a := s.account(active)
	added := false
	if requestErr == nil {
		if a == nil {
			a = &Account{UUID: active, NeedsChatSync: true}
			s.st.Accounts = append(s.st.Accounts, a)
			added = true
		} else {
			previousAccount = *a
		}
		if usageOnly {
			a.Usage = usage
		} else {
			a.Org = p.Organization.UUID
			a.Email = p.Account.Email
			a.Name = p.Account.DisplayName
			a.Plan = p.plan()
		}
		a.Problem = ""
	}
	delete(s.st.CheckPending, active)
	if err = s.saveState(); err != nil {
		s.st.CheckPending[active] = true
		if added {
			s.st.Accounts = s.st.Accounts[:len(s.st.Accounts)-1]
		} else if requestErr == nil {
			*a = previousAccount
		}
		return "", fmt.Errorf("检查结果未能保存，已暂停后续请求：%w", err)
	}
	if requestErr != nil {
		if limited != nil {
			return "", fmt.Errorf("服务器限流，请在 %s 后重试", s.st.RateLimitedUntil[active].Local().Format("15:04:05"))
		}
		return "", requestErr
	}
	s.active = active
	if usageOnly {
		return "当前账号额度已更新", nil
	}
	return a.Email, nil
}
