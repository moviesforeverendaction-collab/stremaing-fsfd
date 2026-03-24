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

type Manager struct {
	log     *zap.Logger
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	mu      sync.Mutex
	workDir string
	siteDir string
	pyExe   string
	stopped bool
}

var instance *Manager

func Start(log *zap.Logger) {
	log = log.Named("PyBot")
	m := &Manager{log: log}
	instance = m
	go m.run()
}

func Stop() {
	if instance != nil {
		instance.stop()
	}
}

func (m *Manager) run() {
	pyExe, err := resolvePython()
	if err != nil {
		m.log.Error("Python not found on PATH — skipping Python UI bot", zap.Error(err))
		return
	}
	m.pyExe = pyExe
	m.log.Sugar().Infof("Using Python: %s", pyExe)

	workDir, err := m.extractFiles()
	if err != nil {
		m.log.Error("Failed to extract Python bot files", zap.Error(err))
		return
	}
	m.workDir = workDir

	siteDir := filepath.Join(workDir, "site-packages")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		m.log.Error("Failed to create site-packages dir", zap.Error(err))
		return
	}
	m.siteDir = siteDir

	if err := m.installDeps(workDir, siteDir); err != nil {
		m.log.Error("Failed to install Python dependencies", zap.Error(err))
		return
	}

	for {
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		m.log.Info("Starting Python UI bot subprocess")
		if err := m.launch(workDir, siteDir); err != nil {
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
	exe, err := os.Executable()
	if err != nil {
		exe = "."
	}
	pyDir := filepath.Join(filepath.Dir(exe), "pybot")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir pybot: %w", err)
	}
	if err := os.WriteFile(filepath.Join(pyDir, "bot.py"), botPySource, 0o644); err != nil {
		return "", fmt.Errorf("write bot.py: %w", err)
	}
	if err := os.WriteFile(filepath.Join(pyDir, "requirements.txt"), requirementsTxt, 0o644); err != nil {
		return "", fmt.Errorf("write requirements.txt: %w", err)
	}
	m.log.Sugar().Infof("Python bot files extracted to %s", pyDir)
	return pyDir, nil
}

func (m *Manager) installDeps(workDir, siteDir string) error {
	m.log.Sugar().Infof("Installing Python dependencies into %s ...", siteDir)
	cmd := exec.Command(m.pyExe,
		"-m", "pip", "install",
		"-r", filepath.Join(workDir, "requirements.txt"),
		"--target", siteDir,
		"--quiet",
		"--disable-pip-version-check",
		"--no-warn-script-location",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip install: %w", err)
	}
	m.log.Info("Python dependencies installed successfully")
	return nil
}

func (m *Manager) launch(workDir, siteDir string) error {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, m.pyExe, filepath.Join(workDir, "bot.py"))
	cmd.Env = m.buildEnv(siteDir)

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

// buildEnv builds a clean env for the subprocess.
// FIX: allocate a fresh slice instead of env[:0] which aliased the same
// backing array and corrupted itself while iterating.
func (m *Manager) buildEnv(siteDir string) []string {
	cfg := config.ValueOf

	// Start from a clean copy — never slice the original
	base := os.Environ()

	// overrides: key → value (applied last, wins over base)
	overrides := map[string]string{
		// THE critical fix: tell Python exactly where kurigram lives
		"PYTHONPATH":   siteDir,
		"PYBOT_SITE":   siteDir,
		"PYBOT_WORKDIR": m.workDir,
		"API_ID":       fmt.Sprintf("%d", cfg.ApiID),
		"API_HASH":     cfg.ApiHash,
		"BOT_TOKEN":    cfg.BotToken,
		"HOST":         cfg.Host,
	}

	// Copy optional vars from parent env
	for _, key := range []string{"ADMIN_IDS", "SUPPORT_LINK", "ABOUT_LINK", "DEVELOPER_LINK"} {
		if v := os.Getenv(key); v != "" {
			overrides[key] = v
		}
	}

	// If PYTHONPATH already exists in the environment, prepend siteDir to it
	for _, e := range base {
		if strings.HasPrefix(e, "PYTHONPATH=") {
			existing := strings.TrimPrefix(e, "PYTHONPATH=")
			if existing != "" {
				overrides["PYTHONPATH"] = siteDir + string(os.PathListSeparator) + existing
			}
			break
		}
	}

	// Build final env: base entries not overridden + all overrides
	result := make([]string, 0, len(base)+len(overrides))
	for _, e := range base {
		key := e[:strings.IndexByte(e, '=')]
		if _, overridden := overrides[key]; !overridden {
			result = append(result, e)
		}
	}
	for k, v := range overrides {
		result = append(result, k+"="+v)
	}
	return result
}

func resolvePython() (string, error) {
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		out, err := exec.Command(path, "--version").Output()
		if err == nil && strings.HasPrefix(string(out), "Python 3") {
			return path, nil
		}
	}
	return "", fmt.Errorf("no Python 3 interpreter found on PATH")
}
