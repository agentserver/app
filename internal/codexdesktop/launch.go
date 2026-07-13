package codexdesktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

type Launcher func(context.Context, string) error
type ProcessSnapshot map[uint32]struct{}
type protocolActivator func(context.Context, Detected, string) error
type processSnapshotter func(context.Context, Detected) (ProcessSnapshot, error)
type sleepFunc func(context.Context, time.Duration) error

type launchOptions struct {
	detect       func() (Detected, error)
	activate     protocolActivator
	snapshot     processSnapshotter
	timeout      time.Duration
	pollInterval time.Duration
	sleep        sleepFunc
}

func ThreadURL(folder string) string {
	if folder == "" {
		return "codex://threads/new"
	}
	q := url.Values{}
	q.Set("path", folder)
	return "codex://threads/new?" + q.Encode()
}

func launchWithOptions(ctx context.Context, folder string, opts launchOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if opts.detect == nil {
		return launchFailedError("缺少 detector 安全依赖", errors.New("detector is nil"))
	}
	if opts.activate == nil {
		return launchFailedError("缺少 activator 安全依赖", errors.New("activator is nil"))
	}
	if opts.snapshot == nil {
		return launchFailedError("缺少 snapshotter 安全依赖", errors.New("snapshotter is nil"))
	}

	det, err := opts.detect()
	if err != nil {
		return launchPreflightError(det, err)
	}
	if err := validateDetected(det); err != nil {
		return newSafeError(
			"启动前无法验证 "+ShortDisplayName+" 的 AppUserModelID 包身份。",
			err,
		)
	}

	timeout := opts.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	totalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	baseline, err := opts.snapshot(totalCtx, det)
	if err != nil {
		return launchFailedError("无法读取启动前的可信应用进程", err)
	}
	if err := activateWithDeadline(totalCtx, opts.activate, det, ThreadURL(folder)); err != nil {
		return launchFailedError("Windows 无法直接激活已验证的 codex:// 应用", err)
	}

	pollInterval := opts.pollInterval
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	sleep := opts.sleep
	if sleep == nil {
		sleep = sleepWithContext
	}

	var previousPostLaunch ProcessSnapshot
	for {
		current, err := opts.snapshot(totalCtx, det)
		if err != nil {
			return launchFailedError("无法确认可信应用进程", err)
		}
		if snapshotsContainNewProcess(baseline, current) ||
			(snapshotsOverlap(baseline, current) && snapshotsOverlap(previousPostLaunch, current)) {
			return nil
		}
		previousPostLaunch = current
		if err := sleep(totalCtx, pollInterval); err != nil {
			return launchFailedError("直接激活请求已提交，但无法确认可信应用进程已经启动", err)
		}
	}
}

func activateWithDeadline(ctx context.Context, activate protocolActivator, det Detected, rawURL string) error {
	result := make(chan error, 1)
	go func() {
		result <- activate(ctx, det, rawURL)
	}()
	select {
	case err := <-result:
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func snapshotsContainNewProcess(baseline, current ProcessSnapshot) bool {
	for pid := range current {
		if _, existed := baseline[pid]; !existed {
			return true
		}
	}
	return false
}

func launchPreflightError(det Detected, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return newSafeError(
			fmt.Sprintf("未检测到%s；请从 Microsoft Store 安装，或运行 winget install --id=%s --source=msstore", LongDisplayName, CodexStoreProductID),
			err,
		)
	case errors.Is(err, ErrSchemeMissing), errors.Is(err, ErrSchemeTargetInvalid):
		return repairRequiredError(err)
	default:
		return newSafeError(
			"启动前无法检查"+LongDisplayName+"。请重试；若仍失败，请重新运行安装向导。",
			err,
		)
	}
}

func launchFailedError(detail string, cause error) error {
	return newSafeError(
		fmt.Sprintf("%s 桌面应用本身无法启动：%s。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall", ShortDisplayName, detail),
		ErrLaunchFailed,
		cause,
	)
}

func snapshotsOverlap(a, b ProcessSnapshot) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for pid := range b {
		if _, ok := a[pid]; ok {
			return true
		}
	}
	return false
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
