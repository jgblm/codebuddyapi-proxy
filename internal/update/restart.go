package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wnddd839/codebuddy-proxy/internal/version"
)

const helperFlag = "--buddy-update-helper"
const readyEnv = "CODEBUDDY_UPDATE_READY"
const resultEnv = "CODEBUDDY_UPDATE_RESULT"

type handoff struct {
	Exe, Stage, Backup, Ready, Target, Dir string
	Args                                   []string
	Parent                                 int
}

type Handoff struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	helper string
}

// Prepare 只复制当前程序作为短命助手；它收到排空许可并等旧进程退出后才替换。
func (m *Manager) Prepare(stage string) (*Handoff, error) {
	helper, err := reserve(filepath.Dir(m.exe), "helper-*.exe")
	if err != nil {
		return nil, err
	}
	src, err := os.Open(m.exe)
	if err != nil {
		os.Remove(helper)
		return nil, err
	}
	defer src.Close()
	dst, err := os.OpenFile(helper, os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		os.Remove(helper)
		return nil, err
	}
	_, copyErr := io.Copy(dst, src)
	err = errors.Join(copyErr, dst.Chmod(0700), dst.Close())
	if err != nil {
		os.Remove(helper)
		return nil, err
	}
	cmd := exec.Command(helper, helperFlag)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	detach(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		os.Remove(helper)
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		os.Remove(helper)
		return nil, err
	}
	m.mu.Lock()
	target := m.release.Tag
	m.mu.Unlock()
	// 记录原工作目录；助手启动新进程时显式设置，避免依赖继承。
	dir, _ := os.Getwd()
	job := handoff{Exe: m.exe, Stage: stage, Target: target, Args: os.Args[1:], Parent: os.Getpid(), Dir: dir}
	if err = json.NewEncoder(input).Encode(job); err != nil {
		input.Close()
		cmd.Process.Kill()
		cmd.Wait()
		os.Remove(helper)
		return nil, err
	}
	return &Handoff{cmd: cmd, input: input, helper: helper}, nil
}

// Commit 必须在现有 Shutdown 成功后调用。管道仍保持打开，随旧进程退出关闭。
func (h *Handoff) Commit() error { _, err := h.input.Write([]byte{1}); return err }

// RestartOriginal 用于监听已关闭但排空失败的情况；助手只重启原程序，不替换文件。
func (h *Handoff) RestartOriginal(message string) error {
	if _, err := h.input.Write([]byte{2}); err != nil {
		return err
	}
	return json.NewEncoder(h.input).Encode(message)
}

func (h *Handoff) Abort() {
	h.input.Close()
	h.cmd.Process.Kill()
	h.cmd.Wait()
	os.Remove(h.helper)
}

func reserve(dir, kind string) (string, error) {
	f, err := os.CreateTemp(dir, ".buddy-update-"+kind)
	if err != nil {
		return "", err
	}
	return f.Name(), f.Close()
}

// replaceWaits 是 Windows 上文件仍被占用时的退避表；声明为变量方便测试缩短。
var replaceWaits = []time.Duration{
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
	800 * time.Millisecond,
	1500 * time.Millisecond,
}

// renameRetry 只在“文件被占用”时按退避表重试；其余错误立即返回，避免掩盖真实问题。
func renameRetry(oldpath, newpath string) error {
	err := os.Rename(oldpath, newpath)
	for i := 0; i < len(replaceWaits) && heldOpen(err); i++ {
		time.Sleep(replaceWaits[i])
		err = os.Rename(oldpath, newpath)
	}
	return err
}

func replace(stage, exe, backup string) error {
	if st, err := os.Stat(stage); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("待替换文件不存在或无效：%v", err)
	}
	// backup 是本次助手独占创建的空占位文件，不复用任何历史备份。
	if st, err := os.Lstat(backup); err != nil || !st.Mode().IsRegular() || st.Size() != 0 {
		return errors.New("备份占位文件已变化，拒绝覆盖")
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := renameRetry(exe, backup); err != nil {
		return fmt.Errorf("移动原程序失败，未替换：%w", err)
	}
	if err := renameRetry(stage, exe); err != nil {
		// 恢复同样有限重试；失败时原文件仍以备份形式保留，绝不丢弃。
		restore := renameRetry(backup, exe)
		return fmt.Errorf("替换失败：%w；恢复原程序结果：%v", err, restore)
	}
	return nil
}

// DispatchHelper 必须在加载配置前调用，避免助手触碰账户、环境文件和使用量。
func DispatchHelper() bool {
	if len(os.Args) != 2 || os.Args[1] != helperFlag {
		return false
	}
	err := runHelper()
	if err != nil {
		fmt.Fprintln(os.Stderr, "手动更新：", err)
	}
	return true
}

func runHelper() (err error) {
	var job handoff
	dec := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
	if err = dec.Decode(&job); err != nil {
		return err
	}
	// JSON 解码器可能预读许可字节，因此继续读取它自己的缓冲区。
	gate := io.MultiReader(dec.Buffered(), os.Stdin)
	var permission [1]byte
	if _, err = io.ReadFull(gate, permission[:]); err != nil {
		return err
	}
	// Encoder 写入的换行也在缓冲区中。
	for permission[0] == '\n' || permission[0] == '\r' {
		if _, err = io.ReadFull(gate, permission[:]); err != nil {
			return err
		}
	}
	if permission[0] != 1 && permission[0] != 2 {
		return errors.New("更新未获得排空许可")
	}
	var restartMessage string
	if permission[0] == 2 {
		if err = json.NewDecoder(io.LimitReader(gate, 1<<20)).Decode(&restartMessage); err != nil {
			return err
		}
	}
	if err = waitParent(job.Parent, 30*time.Second); err != nil {
		return err
	}
	self, _ := os.Executable()
	defer os.Remove(self) // Unix 可直接移除；Windows 留下本次专用助手供手动清理。
	defer os.Remove(job.Stage)
	diagnostic, err := os.OpenFile(self+".log", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer diagnostic.Close()
	defer func() {
		if err != nil {
			fmt.Fprintln(diagnostic, err)
		}
	}()
	dir := filepath.Dir(job.Exe)
	if permission[0] == 2 {
		if job.Ready, err = reserve(dir, "ready-*"); err != nil {
			return err
		}
		defer os.Remove(job.Ready)
		if _, err = launchReady(job, diagnostic, restartMessage); err != nil {
			return fmt.Errorf("排空失败后重启原程序失败：%w", err)
		}
		fmt.Fprintln(diagnostic, restartMessage)
		return nil
	}
	if job.Backup, err = reserve(dir, "backup-*"); err != nil {
		return err
	}
	if job.Ready, err = reserve(dir, "ready-*"); err != nil {
		return err
	}
	defer os.Remove(job.Ready)
	if err = replace(job.Stage, job.Exe, job.Backup); err != nil {
		// 移动失败时原文件仍在；替换失败时 replace 已尝试恢复。
		_, startErr := launchReady(job, diagnostic, "")
		return fmt.Errorf("%w；原程序重新启动：%v", err, startErr)
	}
	pid, startErr := launchReady(job, diagnostic, "")
	if startErr == nil {
		fmt.Fprintf(diagnostic, "更新成功：%s；备份：%s\n", job.Target, job.Backup)
		return nil
	}
	if pid != 0 {
		return fmt.Errorf("新版本未确认就绪且无法停止 PID %d：%w；拒绝启动第二个服务；备份：%s", pid, startErr, job.Backup)
	}
	// launchReady 已停止并等待失败子进程，恢复前绝不保留第二个服务进程。
	failed, allocErr := reserve(dir, "failed-*")
	if allocErr != nil {
		return fmt.Errorf("新版本启动失败：%w；无法准备回滚：%v；备份：%s", startErr, allocErr, job.Backup)
	}
	os.Remove(failed)
	// 回滚同样只对被占用做有限重试，避免新 exe 刚退出时被短暂锁定。
	if err = renameRetry(job.Exe, failed); err != nil {
		return fmt.Errorf("新版本启动失败：%w；移开失败文件：%v；备份：%s", startErr, err, job.Backup)
	}
	if err = renameRetry(job.Backup, job.Exe); err != nil {
		return fmt.Errorf("新版本启动失败：%w；恢复备份失败：%v；备份：%s", startErr, err, job.Backup)
	}
	os.Remove(failed)
	message := fmt.Sprintf("新版本启动失败，已恢复原程序：%v", startErr)
	if _, err = launchReady(job, diagnostic, message); err != nil {
		return fmt.Errorf("%s；回滚启动结果：%v", message, err)
	}
	// 回滚成功：旧版本已重新监听，提示经 resultEnv 交给新进程展示给管理台。
	return nil
}

func launchReady(job handoff, log *os.File, result string) (int, error) {
	if err := os.Truncate(job.Ready, 0); err != nil {
		return 0, err
	}
	cmd := exec.Command(job.Exe, job.Args...)
	cmd.Env = append(os.Environ(), readyEnv+"="+job.Ready, resultEnv+"="+result)
	// 保留原工作目录，保证相对路径的 .env / 数据文件仍能解析。
	if job.Dir != "" {
		cmd.Dir = job.Dir
	}
	cmd.Stdout, cmd.Stderr = log, log
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return 0, fmt.Errorf("子进程在监听就绪前退出：%v", err)
		case <-tick.C:
			if b, err := os.ReadFile(job.Ready); err == nil && len(b) > 0 {
				return cmd.Process.Pid, nil
			}
		case <-timer.C:
			killErr := cmd.Process.Kill()
			if killErr != nil {
				return cmd.Process.Pid, fmt.Errorf("等待监听就绪超时且停止子进程失败：%w", killErr)
			}
			<-done
			return 0, errors.New("等待本地监听就绪超时（20 秒），失败子进程已停止")
		}
	}
}

// ReportReady 仅在本地监听成功后写本次助手创建的私有占位文件。
func ReportReady() error {
	path := os.Getenv(readyEnv)
	os.Unsetenv(readyEnv)
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(version.Version)
	return errors.Join(err, f.Close())
}
