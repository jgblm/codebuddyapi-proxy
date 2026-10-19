package update

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// 进程级测试用环境变量在同一个测试二进制里切换角色：
// helper 复制 runHelper 的真实逻辑，child 模拟被替换后的新版本，sleeper 充当旧进程。
const (
	testModeEnv   = "CBP_TEST_MODE"
	testFailEnv   = "CBP_TEST_FAIL"
	testStateEnv  = "CBP_TEST_STATE"
	testResultEnv = "CBP_TEST_RESULT"
)

// childResult 由模拟的新版本写出，用来证明参数与工作目录被保留。
type childResult struct {
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
	Message string   `json:"message"`
}

func TestMain(m *testing.M) {
	switch os.Getenv(testModeEnv) {
	case "helper":
		// 先改成 child 模式，助手经 launchReady 启动的新进程才不会再次进入助手分支。
		os.Setenv(testModeEnv, "child")
		if err := runHelper(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "child":
		os.Exit(fakeChild())
	case "sleeper":
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}

// fakeChild 是新版本的替身：首轮可按 CBP_TEST_FAIL 故意失败，后续写入结果与就绪标记。
func fakeChild() int {
	ready := os.Getenv(readyEnv)
	if ready == "" {
		return 2
	}
	if os.Getenv(testFailEnv) != "" {
		state := os.Getenv(testStateEnv)
		if state == "" {
			return 2
		}
		if _, err := os.Stat(state); err != nil {
			// 模拟新版本启动即崩溃：留下状态文件供回滚后的第二轮识别，且不写就绪标记。
			if err := os.WriteFile(state, []byte("first launch failed\n"), 0600); err != nil {
				return 3
			}
			return 1
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return 3
	}
	out, err := json.Marshal(childResult{Args: os.Args[1:], Cwd: cwd, Message: os.Getenv(resultEnv)})
	if err != nil {
		return 3
	}
	result := os.Getenv(testResultEnv)
	if result == "" {
		return 2
	}
	if err := os.WriteFile(result, out, 0600); err != nil {
		return 3
	}
	if err := os.WriteFile(ready, []byte("v0.6-test"), 0600); err != nil {
		return 3
	}
	// 真实服务写就绪后会继续运行；测试替身留在原地直到助手观察到并删除就绪文件，
	// 否则进程可能在 helper 的 50ms 轮询前退出，造成假失败。
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(ready); os.IsNotExist(err) {
			break
		}
	}
	return 0
}

func TestProcessUpdateReplacesAndRestarts(t *testing.T) {
	runProcessScenario(t, false, false)
}
func TestProcessUpdateRollsBackOnFailedStart(t *testing.T) {
	runProcessScenario(t, true, false)
}
func TestProcessUpdateDrainFailureRestartsOriginal(t *testing.T) {
	runProcessScenario(t, false, true)
}

// runProcessScenario 真实走一遍 helper → replace → 启动新进程 → 就绪/回滚。
func runProcessScenario(t *testing.T, rollback, restartOriginal bool) {
	t.Helper()
	d := t.TempDir()
	// 新进程退出与 TempDir 清理存在竞态：Windows 上运行中的 exe 无法删除。
	// 该清理在 t.TempDir 之后注册，按 LIFO 先于删目录执行，等新进程释放文件。
	t.Cleanup(func() { waitUnlocked(t, filepath.Join(d, "exe"+exeSuffix())) })
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	// 旧程序带一段附加标记，新程序是未改动的测试二进制。
	// 附加数据不影响执行，让替换、回滚及只重启原版本的断言可区分。
	oldBytes := append(slices.Clone(bin), []byte("\nold-build-marker\n")...)
	newBytes := bin
	if restartOriginal {
		// 排空失败后绝不能安装或执行暂存文件，即使该文件不是可执行程序。
		newBytes = []byte("must not be installed after drain failure")
	}
	exe, stage := filepath.Join(d, "exe"+exeSuffix()), filepath.Join(d, "stage"+exeSuffix())
	helper := filepath.Join(d, "helper"+exeSuffix())
	for path, data := range map[string][]byte{exe: oldBytes, stage: newBytes, helper: bin} {
		if err := os.WriteFile(path, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	state, result := filepath.Join(d, "state"), filepath.Join(d, "result.json")
	env := append(os.Environ(), testModeEnv+"=helper", testResultEnv+"="+result, testStateEnv+"="+state)
	if rollback {
		env = append(env, testFailEnv+"=1")
	}

	// 旧进程替身：helper 会等它退出后才替换文件。
	sleeper := exec.Command(self)
	sleeper.Env = append(os.Environ(), testModeEnv+"=sleeper")
	sleeper.Stdout, sleeper.Stderr = os.Stdout, os.Stderr
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })

	cmd := exec.Command(helper)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	job := handoff{
		Exe: exe, Stage: stage, Target: "v0.6-test",
		Args: []string{"--foo", "bar"}, Parent: sleeper.Process.Pid, Dir: d,
	}
	if err := json.NewEncoder(stdin).Encode(job); err != nil {
		t.Fatal(err)
	}
	message := "更新排空失败，已取消更新并重新启动原程序：context deadline exceeded"
	if restartOriginal {
		err = (&Handoff{input: stdin}).RestartOriginal(message)
	} else {
		_, err = stdin.Write([]byte{1})
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	// 许可已写入；结束旧进程替身，helper 的 waitParent 随即返回。
	_ = sleeper.Process.Kill()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			logText, _ := os.ReadFile(helper + ".log")
			t.Fatalf("helper failed: %v\nlog: %s", err, logText)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("helper did not exit in 40s")
	}

	// 备份由 reserve 以 .buddy-update-backup-* 命名。
	backups, _ := filepath.Glob(filepath.Join(d, ".buddy-update-backup-*"))
	if rollback || restartOriginal {
		// 回滚会消耗备份；仅重启原程序则根本不应创建备份。
		if b, err := os.ReadFile(exe); err != nil || !slices.Equal(b, oldBytes) {
			t.Fatalf("original binary not preserved: %v", err)
		}
		if len(backups) != 0 {
			t.Fatalf("unexpected backup files: %v", backups)
		}
	} else {
		if b, err := os.ReadFile(exe); err != nil || !slices.Equal(b, newBytes) {
			t.Fatalf("installed binary mismatch: %v", err)
		}
		if len(backups) != 1 {
			t.Fatalf("want exactly one backup, got %v", backups)
		}
		if b, err := os.ReadFile(backups[0]); err != nil || !slices.Equal(b, oldBytes) {
			t.Fatalf("backup does not hold the original binary: %v", err)
		}
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("staged file still present: %v", err)
	}
	// 第二轮（回滚场景）或唯一一轮（成功场景）的新进程写出结果文件。
	raw, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("child did not write result: %v", err)
	}
	var got childResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Args, []string{"--foo", "bar"}) {
		t.Fatalf("args not preserved: %v", got.Args)
	}
	if !sameDir(got.Cwd, d) {
		t.Fatalf("cwd not preserved: %q != %q", got.Cwd, d)
	}
	if restartOriginal && got.Message != message {
		t.Fatalf("drain failure message not preserved: %q", got.Message)
	}
}

func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// exeSuffix 返回平台可执行文件后缀；Windows 上缺少 .exe 会退回 PATH 查找。
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// waitUnlocked 在新进程退出后等待其 exe 可重命名，避免 TempDir 清理与映像卸载竞态。
func waitUnlocked(t *testing.T, path string) {
	t.Helper()
	probe := path + ".cleanup-probe"
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if err := os.Rename(path, probe); err == nil {
			_ = os.Rename(probe, path)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
