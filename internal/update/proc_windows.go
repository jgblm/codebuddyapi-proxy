package update

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}

// heldOpen 判断重命名失败是否只是文件仍被占用（杀软/OneDrive/索引服务暂时持锁）。
// 32 为共享冲突、33 为锁冲突；其余错误（如权限、路径不存在）应立即失败。
func heldOpen(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33))
}

func waitParent(pid int, timeout time.Duration) error {
	h, err := syscall.OpenProcess(0x00100000, false, uint32(pid))
	if err != nil {
		if errors.Is(err, syscall.Errno(87)) {
			return nil
		}
		return err
	}
	defer syscall.CloseHandle(h)
	event, err := syscall.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
	if err != nil {
		return err
	}
	if event != 0 {
		return errors.New("等待旧进程退出超时；未替换程序")
	}
	return nil
}
