package pybot

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"EverythingSuckz/fsb/config"

	"go.uber.org/zap"
)

//go:embed bot.py
var botPySource []byte

//go:embed requirements.txt
var requirementsTxt []byte

// Manager handles the lifecycle of the embedded Python bot subprocess.
type Manager struct {
	log     *zap.Logger
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	mu      sync.Mutex
	workDir string
	stopped bool
}

var instance *Manager

// Start extracts bot.py, installs dependencies, and launches the Python bot.
// It is called once from run.go after Go bot initialisation.
func Start(log *zap.Logger) {
	log = log.Named("PyBot")
	m := &Manager{log: log}
	instance = m
	go m.run()
}

// Stop gracefully terminates the Python subprocess.
func Stop() {
	if instance != nil {
		instance.stop()
	}
}

// ── internal ──────────────────────────────────────────────────────────────────

func (m *Manager) run() {
	// Write bot.py and requirements.txt next to the binary
	workDir, err := m.extractFiles()
	if err != nil {
		m.log.Error("Failed to extract Python bot files", zap.Error(err))
		return
	}
	m.workDir = workDir

	// Install Python dependencies once
	if err := m.installDeps(workDir); err != nil {
		m.log.Error("Failed to install Python dependencies", zap.Error(err))
		return
	}

	// Restart loop — if Python bot crashes, restart after a short delay
	for {
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		m.log.Info("Starting Python UI bot subprocess")
		if err := m.launch(workDir); err != nil {
			m.log.Error("Python bot exited with error", zap.Error(err))
		}

		m.mu.Lock()
		stopped := m.stopped
		m.mu.Unlock()
		if stopped {
			return
		}

		m.log.Warn("Python bot stopped unexpectedly, restarting in 5s...")
		time.Sleep(5 * time.Second)
	}
}

func (m *Manager) extractFiles() (string, error) {
	// Write alongside the running binary
	exe, err := os.Executable()
	if err != nil {
		exe = "."
	}
	dir := filepath.Dir(exe)
	pyDir := filepath.Join(dir, "pybot")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir pybot: %w", err)
	}

	botPath := filepath.Join(pyDir, "bot.py")
	if err := os.WriteFile(botPath, botPySource, 0o644); err != nil {
		return "", fmt.Errorf("write bot.py: %w", err)
	}

	reqPath := filepath.Join(pyDir, "requirements.txt")
	if err := os.WriteFile(reqPath, requirementsTxt, 0o644); err != nil {
		return "", fmt.Errorf("write requirements.txt: %w", err)
	}

	m.log.Sugar().Infof("Python bot files extracted to %s", pyDir)
	return pyDir, nil
}

func (m *Manager) installDeps(workDir string) error {
	m.log.Info("Installing Python dependencies (kurigram)...")
	pip := findPython("-m", "pip")
	cmd := exec.Command(pip[0], append(pip[1:],
		"install", "-r", filepath.Join(workDir, "requirements.txt"), "-q")...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip install: %w", err)
	}
	m.log.Info("Python dependencies installed")
	return nil
}

func (m *Manager) launch(workDir string) error {
	ctx, cancel := context.WithCancel(context.Background())

	py := findPython()
	args := append(py[1:], filepath.Join(workDir, "bot.py"))
	cmd := exec.CommandContext(ctx, py[0], args...)

	// Pass all necessary env vars to the subprocess
	cmd.Env = buildEnv()

	// Stream stdout/stderr through the Go logger
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	m.mu.Lock()
	m.cmd = cmd
	m.cancel = cancel
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start python: %w", err)
	}

	go m.streamLogs(stdout, false)
	go m.streamLogs(stderr, true)

	err := cmd.Wait()
	cancel()
	return err
}

func (m *Manager) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	if m.cancel != nil {
		m.cancel()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Signal(os.Interrupt)
		time.Sleep(2 * time.Second)
		_ = m.cmd.Process.Kill()
	}
}

func (m *Manager) streamLogs(r io.Reader, isErr bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if isErr || strings.Contains(strings.ToLower(line), "error") {
			m.log.Sugar().Warnf("[pybot] %s", line)
		} else {
			m.log.Sugar().Infof("[pybot] %s", line)
		}
	}
}

// buildEnv builds the subprocess environment from the Go config.
func buildEnv() []string {
	cfg := config.ValueOf
	env := os.Environ() // inherit current environment

	set := func(k, v string) {
		env = append(env, k+"="+v)
	}

	set("API_ID", fmt.Sprintf("%d", cfg.ApiID))
	set("API_HASH", cfg.ApiHash)
	set("BOT_TOKEN", cfg.BotToken)
	set("HOST", cfg.Host)

	// Pass ADMIN_IDS from env if set
	if v := os.Getenv("ADMIN_IDS"); v != "" {
		set("ADMIN_IDS", v)
	}
	if v := os.Getenv("SUPPORT_LINK"); v != "" {
		set("SUPPORT_LINK", v)
	}
	if v := os.Getenv("ABOUT_LINK"); v != "" {
		set("ABOUT_LINK", v)
	}
	if v := os.Getenv("DEVELOPER_LINK"); v != "" {
		set("DEVELOPER_LINK", v)
	}

	// Tell Python bot where to store its session file
	set("PYBOT_WORKDIR", ".")

	return env
}

// findPython finds the Python 3 interpreter on PATH.
// Returns ["python3", "-m", ...] or ["python", ...] with fallback args.
func findPython(extraArgs ...string) []string {
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return append([]string{path}, extraArgs...)
		}
	}
	// Last resort — let the OS resolve it
	return append([]string{"python3"}, extraArgs...)
}
