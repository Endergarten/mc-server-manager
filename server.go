package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Run struct {
	WorldID         string `json:"worldId"`
	State           string `json:"state"`
	PID             int    `json:"pid"`
	Error           string `json:"error,omitempty"`
	cmd             *exec.Cmd
	input           io.WriteCloser
	done            chan struct{}
	writeMu         sync.Mutex
	file            *os.File
	onlineOperators []string
}
type RunStatus struct {
	WorldID string `json:"worldId"`
	State   string `json:"state"`
	PID     int    `json:"pid"`
	Error   string `json:"error,omitempty"`
}

func (a *App) statusLocked() RunStatus {
	if a.run == nil {
		return RunStatus{State: "stopped"}
	}
	return RunStatus{a.run.WorldID, a.run.State, a.run.PID, a.run.Error}
}
func (a *App) addLog(line string) { a.mu.Lock(); defer a.mu.Unlock(); a.addLogLocked(line) }
func (a *App) addLogLocked(line string) {
	a.logs = append(a.logs, time.Now().Format("15:04:05")+" "+line)
	if len(a.logs) > 800 {
		a.logs = append([]string{}, a.logs[len(a.logs)-800:]...)
	}
}
func (a *App) startWorld(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return fmt.Errorf("管理器正在退出，无法启动新的服务器")
	}
	if a.run != nil && a.run.State != "stopped" && a.run.State != "failed" {
		return fmt.Errorf("已有地图在运行，请先正常停止后切换")
	}
	world, e := a.findLocked(id)
	if e != nil {
		return e
	}
	if world.Engine != "" && world.Engine != "vanilla" {
		return fmt.Errorf("当前版本暂不支持 %s 服务端", world.Engine)
	}
	s := a.db.Settings
	if e = validateSettings(s); e != nil {
		return e
	}
	if !s.EULA {
		return fmt.Errorf("请先到设置阅读并同意 Minecraft EULA")
	}
	if !a.installed(world.Version) {
		return fmt.Errorf("缺少 %s 服务端，请先下载或导入对应 JAR", world.Version)
	}
	if _, e = os.Stat(filepath.Join(a.instance(id), "world", "level.dat")); e != nil {
		return fmt.Errorf("地图文件不完整：%w", e)
	}
	if e = a.verifyInstalled(world.Version); e != nil {
		return e
	}
	probe, e := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if e != nil {
		return fmt.Errorf("端口 %d 已被占用，请停止其他服务端或修改端口", s.Port)
	}
	probe.Close()
	dir := a.instance(id)
	if e = prepareServer(dir, s); e != nil {
		return e
	}
	java := s.Java
	javaDescription := "手动指定 Java"
	if s.JavaMode == "auto" {
		req, e := a.javaRequirement(world.Version)
		if e != nil {
			return e
		}
		chosen, e := chooseJava(a.javas, req)
		if e != nil {
			return e
		}
		java = chosen.Path
		javaDescription = fmt.Sprintf("自动选择 Java %s · %s · %s", chosen.Version, req.Source, chosen.Path)
	}
	cmd := exec.Command(java, "-Xms512M", fmt.Sprintf("-Xmx%dG", s.Memory), "-jar", a.serverJar(world.Version), "nogui")
	cmd.Dir = dir
	hideWindow(cmd)
	input, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	file, e := os.OpenFile(filepath.Join(dir, "manager-console.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if e != nil {
		input.Close()
		return e
	}
	run := &Run{WorldID: id, State: "starting", cmd: cmd, input: input, done: make(chan struct{}), file: file}
	if s.launchOptions().OnlineMode {
		run.onlineOperators = append([]string{}, s.Operators...)
	}
	output := &lineWriter{a: a, run: run}
	cmd.Stdout = output
	cmd.Stderr = output
	if e = cmd.Start(); e != nil {
		input.Close()
		file.Close()
		return fmt.Errorf("Java 启动失败，请检查设置中的 Java 路径：%w", e)
	}
	run.PID = cmd.Process.Pid
	a.run = run
	a.logs = []string{}
	a.addLogLocked("正在启动 " + world.Name + " / Minecraft " + world.Version)
	a.addLogLocked(javaDescription)
	go func() {
		err := cmd.Wait()
		output.flush()
		input.Close()
		file.Close()
		a.mu.Lock()
		if a.run == run {
			if err == nil && run.State == "starting" {
				err = fmt.Errorf("服务端在准备完成前退出，请检查日志")
			}
			if err != nil {
				run.State = "failed"
				run.Error = err.Error()
				a.addLogLocked("服务端已退出：" + err.Error() + "，请查看上方日志（常见原因是 Java 版本不符）")
			} else {
				run.State = "stopped"
				a.addLogLocked("服务器已正常停止")
			}
		}
		close(run.done)
		a.mu.Unlock()
	}()
	return nil
}
func prepareServer(dir string, s Settings) error {
	path := filepath.Join(dir, "server.properties")
	old, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	opts := s.launchOptions()
	values := map[string]string{"level-name": "world", "online-mode": strconv.FormatBool(opts.OnlineMode), "enable-command-block": strconv.FormatBool(opts.CommandBlocks), "allow-flight": strconv.FormatBool(opts.AllowFlight), "white-list": "false", "enforce-whitelist": "false", "server-port": strconv.Itoa(s.Port)}
	text := patchProperties(string(old), values)
	if e = atomicWrite(path, []byte(text)); e != nil {
		return e
	}
	if e = atomicWrite(filepath.Join(dir, "eula.txt"), []byte("eula=true\n")); e != nil {
		return e
	}
	opsPath := filepath.Join(dir, "ops.json")
	ops := []map[string]any{}
	if b, e := os.ReadFile(opsPath); e == nil {
		var previous []map[string]any
		if e = json.Unmarshal(b, &previous); e != nil {
			return fmt.Errorf("ops.json 损坏，未覆盖：%w", e)
		}
		if e = atomicWrite(filepath.Join(dir, "ops.before-manager.json"), b); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	for _, name := range s.Operators {
		if !opts.OnlineMode {
			ops = append(ops, map[string]any{"uuid": offlineUUID(name), "name": name, "level": 4, "bypassesPlayerLimit": true})
		}
	}
	return atomicJSON(opsPath, ops)
}
func patchProperties(old string, values map[string]string) string {
	lines := strings.Split(strings.ReplaceAll(old, "\r\n", "\n"), "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "!") {
			continue
		}
		key, _, ok := strings.Cut(trim, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if val, exists := values[key]; exists {
			lines[i] = key + "=" + val
			seen[key] = true
		}
	}
	for _, key := range []string{"level-name", "online-mode", "enable-command-block", "allow-flight", "white-list", "enforce-whitelist", "server-port"} {
		if !seen[key] {
			lines = append(lines, key+"="+values[key])
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

type lineWriter struct {
	mu      sync.Mutex
	a       *App
	run     *Run
	pending string
}

func (l *lineWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.run.file.Write(b)
	l.pending += string(b)
	for {
		i := strings.IndexByte(l.pending, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(l.pending[:i], "\r")
		l.pending = l.pending[i+1:]
		l.line(line)
	}
	if len(l.pending) > 65536 {
		l.line(l.pending[:65536])
		l.pending = l.pending[65536:]
	}
	return len(b), nil
}
func (l *lineWriter) line(line string) {
	l.a.mu.Lock()
	defer l.a.mu.Unlock()
	l.a.addLogLocked(line)
	if strings.Contains(line, "Done (") && l.run.State == "starting" {
		l.run.State = "running"
		if len(l.run.onlineOperators) > 0 {
			go l.a.grantOnlineOperators(l.run)
		}
	}
}

// Let the official server resolve online player identities. Offline UUIDs must
// never be reused when authentication is enabled.
func (a *App) grantOnlineOperators(run *Run) {
	for _, name := range run.onlineOperators {
		a.mu.Lock()
		if a.run != run || run.State != "running" {
			a.mu.Unlock()
			return
		}
		a.addLogLocked("> op " + name)
		a.mu.Unlock()
		run.writeMu.Lock()
		_, e := fmt.Fprintln(run.input, "op "+name)
		run.writeMu.Unlock()
		if e != nil {
			a.addLog("自动授权管理员失败：" + e.Error())
			return
		}
	}
}
func (l *lineWriter) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending != "" {
		l.line(l.pending)
		l.pending = ""
	}
}
func (a *App) command(text string) error {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "/")
	if text == "" || len(text) > 4096 || strings.ContainsAny(text, "\r\n\x00") {
		return fmt.Errorf("请输入一条有效的后台命令")
	}
	a.mu.Lock()
	r := a.run
	if r == nil || (r.State != "running" && r.State != "starting") {
		a.mu.Unlock()
		return fmt.Errorf("服务器未运行或正在停止")
	}
	if text == "stop" {
		r.State = "stopping"
	}
	a.addLogLocked("> " + text)
	a.mu.Unlock()
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	_, e := fmt.Fprintln(r.input, text)
	return e
}
func (a *App) stopAndWait(timeout time.Duration) error {
	a.mu.Lock()
	r := a.run
	if r == nil || r.State == "stopped" || r.State == "failed" {
		a.mu.Unlock()
		return nil
	}
	state := r.State
	a.mu.Unlock()
	if state != "stopping" {
		if e := a.command("stop"); e != nil {
			return e
		}
	}
	select {
	case <-r.done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("仍在等待服务器保存，管理器保持运行，请查看后台")
	}
}
