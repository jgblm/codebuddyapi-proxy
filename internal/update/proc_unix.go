//go:build !windows

package update

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// heldOpen 在非 Windows 平台没有“文件被占用”语义：重命名失败一律立即返回。
func heldOpen(error) bool { return false }

func waitParent(pid int, timeout time.Duration) error {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		if os.Getppid() != pid || errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("等待旧进程退出超时；未替换程序")
}
