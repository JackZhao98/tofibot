package executor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ExecuteShell 接受 context、timeout 和 env 参数
func ExecuteShell(parentCtx context.Context, script string, env map[string]string, timeout int) (string, error) {
	// 如果没设置超时，默认给 30 秒，防止永久挂起
	if timeout <= 0 {
		timeout = 30
	}

	// 创建一个带超时的 Context，继承自 parent context
	var ctx context.Context
	var cancel context.CancelFunc

	if parentCtx != nil {
		// 从 parent context 派生，这样既支持超时也支持手动取消
		ctx, cancel = context.WithTimeout(parentCtx, time.Duration(timeout)*time.Second)
	} else {
		// 兼容旧代码：如果没有传入 parent context，使用 Background
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	}
	defer cancel()

	// 使用 CommandContext，这样超时后 Go 会自动 Kill 掉这个子进程
	cmd := exec.CommandContext(ctx, "sh", "-c", script)

	// 注入环境变量

	if len(env) > 0 {

		cmd.Env = append(cmd.Environ(), toEnvList(env)...)

	}

	// 0. 安全检查 (Static Analysis)

	if err := CheckShellSafety(script); err != nil {

		return "", fmt.Errorf("安全检查未通过: %v", err)

	}

	output, err := cmd.CombinedOutput() // 同时获取 stdout 和 stderr
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("执行超时 (%d秒)", timeout)
		}
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// toEnvList 将 map 转换为 KEY=VALUE 格式的切片
func toEnvList(env map[string]string) []string {
	var list []string
	for k, v := range env {
		list = append(list, fmt.Sprintf("%s=%s", k, v))
	}
	return list
}
