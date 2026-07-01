package modulehook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/module"
)

type ConfigPatchState = string

const (
	ConfigPatchName                        = "config_patch"
	ConfigPatchClean      ConfigPatchState = StateClean
	ConfigPatchActive     ConfigPatchState = StateActive
	ConfigPatchRecovered  ConfigPatchState = "recovered"
	ConfigPatchUnresolved ConfigPatchState = "unresolved"
	ConfigPatchFailed     ConfigPatchState = "failed"
)

type BuildDependencies struct {
	WorkerID      string
	WorkerPort    int
	ExternalHooks map[string]ExternalHookRuntime
}

type ConfigPatchOptions struct {
	StateDir    string
	ConfigPath  string
	WorkerID    string
	WorkerPort  int
	PatchedBase string
}

type ConfigPatch struct {
	config   module.ModuleConfig
	options  ConfigPatchOptions
	lockFile *os.File
	state    ConfigPatchState
	detail   map[string]string
}

type configPatchJournal struct {
	ConfigPath     string `json:"config_path"`
	ProviderName   string `json:"provider_name"`
	FieldName      string `json:"field_name"`
	PreviousExists bool   `json:"previous_exists"`
	PreviousValue  string `json:"previous_value"`
	PatchedValue   string `json:"patched_value"`
	WorkerID       string `json:"worker_id"`
	WorkerPort     int    `json:"worker_port"`
	ProcessID      int    `json:"process_id"`
	Timestamp      string `json:"timestamp"`
}

func NewConfigPatch(cfg module.ModuleConfig, deps BuildDependencies) *ConfigPatch {
	config := module.CloneModuleConfig(cfg)
	return &ConfigPatch{
		config:  config,
		options: optionsFromConfig(config, deps),
		state:   ConfigPatchClean,
	}
}

func (p *ConfigPatch) Name() string {
	return ConfigPatchName
}

func (p *ConfigPatch) Config() module.ModuleConfig {
	return module.CloneModuleConfig(p.config)
}

func (p *ConfigPatch) UpdateConfig(cfg module.ModuleConfig) error {
	p.config = module.CloneModuleConfig(cfg)
	p.options = optionsFromConfig(p.config, BuildDependencies{
		WorkerID:   p.options.WorkerID,
		WorkerPort: p.options.WorkerPort,
	})
	return nil
}

func (p *ConfigPatch) State() string {
	return p.state
}

func (p *ConfigPatch) Detail() map[string]string {
	if len(p.detail) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.detail))
	for key, value := range p.detail {
		out[key] = value
	}
	return out
}

func (p *ConfigPatch) Start() error {
	if err := p.acquireLock(); err != nil {
		return err
	}
	if err := p.RecoverStaleJournal(); err != nil {
		_ = p.releaseLock()
		return err
	}
	if p.state == ConfigPatchUnresolved || p.state == ConfigPatchFailed {
		_ = p.releaseLock()
		return fmt.Errorf("config_patch recovery state %s must be resolved before enabling", p.state)
	}

	textBytes, err := os.ReadFile(p.options.ConfigPath)
	if err != nil {
		p.state = ConfigPatchFailed
		_ = p.releaseLock()
		return err
	}
	text := string(textBytes)
	providerName, err := detectModelProvider(text)
	if err != nil {
		p.state = ConfigPatchFailed
		_ = p.releaseLock()
		return err
	}
	current, exists, err := getProviderField(text, providerName, "base_url")
	if err != nil {
		p.state = ConfigPatchFailed
		_ = p.releaseLock()
		return err
	}

	journal := configPatchJournal{
		ConfigPath:     p.options.ConfigPath,
		ProviderName:   providerName,
		FieldName:      "base_url",
		PreviousExists: exists,
		PreviousValue:  current,
		PatchedValue:   p.options.PatchedBase,
		WorkerID:       p.options.WorkerID,
		WorkerPort:     p.options.WorkerPort,
		ProcessID:      os.Getpid(),
		Timestamp:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := p.writeJournal(journal); err != nil {
		p.state = ConfigPatchFailed
		_ = p.releaseLock()
		return err
	}

	next, err := setProviderField(text, providerName, "base_url", p.options.PatchedBase)
	if err != nil {
		p.state = ConfigPatchFailed
		_ = p.releaseLock()
		return err
	}
	if err := atomicWriteTextFile(p.options.ConfigPath, next, 0600); err != nil {
		p.state = ConfigPatchFailed
		_ = p.releaseLock()
		return err
	}

	p.state = ConfigPatchActive
	p.detail = nil
	return nil
}

func (p *ConfigPatch) Stop() error {
	if p.state != ConfigPatchActive {
		return p.releaseLock()
	}
	journal, err := p.readJournal()
	if err != nil {
		p.state = ConfigPatchFailed
		return err
	}
	if err := restoreFromJournal(journal); err != nil {
		p.state = ConfigPatchFailed
		return err
	}
	if err := os.Remove(p.journalPath()); err != nil && !os.IsNotExist(err) {
		p.state = ConfigPatchFailed
		return err
	}
	if err := fsyncDirPath(p.options.StateDir); err != nil {
		p.state = ConfigPatchFailed
		return err
	}
	p.state = ConfigPatchClean
	p.detail = nil
	return p.releaseLock()
}

func (p *ConfigPatch) RecoverStaleJournal() error {
	journal, err := p.readJournal()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		p.state = ConfigPatchFailed
		return err
	}

	textBytes, err := os.ReadFile(journal.ConfigPath)
	if err != nil {
		p.state = ConfigPatchFailed
		return err
	}
	current, exists, err := getProviderField(string(textBytes), journal.ProviderName, journal.FieldName)
	if err != nil {
		p.state = ConfigPatchFailed
		return err
	}
	if exists && current == journal.PatchedValue {
		if err := restoreFromJournal(journal); err != nil {
			p.state = ConfigPatchFailed
			p.detail = recoveryDetail(journal, current)
			return err
		}
		if err := os.Remove(p.journalPath()); err != nil && !os.IsNotExist(err) {
			p.state = ConfigPatchFailed
			p.detail = recoveryDetail(journal, current)
			return err
		}
		if err := fsyncDirPath(p.options.StateDir); err != nil {
			p.state = ConfigPatchFailed
			p.detail = recoveryDetail(journal, current)
			return err
		}
		p.state = ConfigPatchRecovered
		p.detail = recoveryDetail(journal, current)
		return nil
	}

	unresolved := fmt.Sprintf("%s.unresolved.%d", p.journalPath(), time.Now().UnixNano())
	if err := os.Rename(p.journalPath(), unresolved); err != nil {
		p.state = ConfigPatchFailed
		p.detail = recoveryDetail(journal, current)
		return err
	}
	if err := fsyncDirPath(p.options.StateDir); err != nil {
		p.state = ConfigPatchFailed
		p.detail = recoveryDetail(journal, current)
		return err
	}
	p.state = ConfigPatchUnresolved
	p.detail = recoveryDetail(journal, current)
	return nil
}

func (p *ConfigPatch) CloseLockForTest() error {
	return p.releaseLock()
}

func optionsFromConfig(cfg module.ModuleConfig, deps BuildDependencies) ConfigPatchOptions {
	configPath, _ := cfg.Params["config_path"].(string)
	if configPath == "" {
		configPath = "~/.codex/config.toml"
	}
	stateDir, _ := cfg.Params["state_dir"].(string)
	if stateDir == "" {
		stateDir = "~/.ainn"
	}
	return ConfigPatchOptions{
		StateDir:    expandHome(stateDir),
		ConfigPath:  expandHome(configPath),
		WorkerID:    deps.WorkerID,
		WorkerPort:  deps.WorkerPort,
		PatchedBase: fmt.Sprintf("http://%s:%d", constants.LocalhostAddr, deps.WorkerPort),
	}
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

func (p *ConfigPatch) acquireLock() error {
	if err := os.MkdirAll(p.options.StateDir, 0700); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(filepath.Join(p.options.StateDir, "config.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lockFile.Close()
		return fmt.Errorf("config_patch already active on another worker")
	}
	p.lockFile = lockFile
	return nil
}

func (p *ConfigPatch) releaseLock() error {
	if p.lockFile == nil {
		return nil
	}
	err := syscall.Flock(int(p.lockFile.Fd()), syscall.LOCK_UN)
	closeErr := p.lockFile.Close()
	p.lockFile = nil
	if err != nil {
		return err
	}
	return closeErr
}

func (p *ConfigPatch) journalPath() string {
	return filepath.Join(p.options.StateDir, "config-patch-journal.json")
}

func (p *ConfigPatch) writeJournal(journal configPatchJournal) error {
	if err := os.MkdirAll(p.options.StateDir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteBytes(p.journalPath(), data, 0600)
}

func (p *ConfigPatch) readJournal() (configPatchJournal, error) {
	data, err := os.ReadFile(p.journalPath())
	if err != nil {
		return configPatchJournal{}, err
	}
	var journal configPatchJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return configPatchJournal{}, err
	}
	return journal, nil
}

func restoreFromJournal(journal configPatchJournal) error {
	textBytes, err := os.ReadFile(journal.ConfigPath)
	if err != nil {
		return err
	}
	text := string(textBytes)
	var next string
	if journal.PreviousExists {
		next, err = setProviderField(text, journal.ProviderName, journal.FieldName, journal.PreviousValue)
	} else {
		next, err = removeProviderField(text, journal.ProviderName, journal.FieldName)
	}
	if err != nil {
		return err
	}
	return atomicWriteTextFile(journal.ConfigPath, next, 0600)
}

func recoveryDetail(journal configPatchJournal, current string) map[string]string {
	return map[string]string{
		"provider_name":  journal.ProviderName,
		"field_name":     journal.FieldName,
		"previous_value": journal.PreviousValue,
		"patched_value":  journal.PatchedValue,
		"current_value":  current,
	}
}

func detectModelProvider(text string) (string, error) {
	re := regexp.MustCompile(`(?m)^model_provider\s*=\s*"([^"]+)"\s*$`)
	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return "", fmt.Errorf("model_provider not found")
	}
	return match[1], nil
}

func getProviderField(text string, providerName string, fieldName string) (string, bool, error) {
	lines, start, end, err := providerSection(text, providerName)
	if err != nil {
		return "", false, err
	}
	fieldRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(fieldName) + `\s*=\s*"([^"]*)"\s*$`)
	for i := start + 1; i < end; i++ {
		match := fieldRe.FindStringSubmatch(lines[i])
		if len(match) == 2 {
			return match[1], true, nil
		}
	}
	return "", false, nil
}

func setProviderField(text string, providerName string, fieldName string, value string) (string, error) {
	lines, start, end, err := providerSection(text, providerName)
	if err != nil {
		return "", err
	}
	fieldRe := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(fieldName) + `\s*=\s*"([^"]*)"\s*$`)
	for i := start + 1; i < end; i++ {
		match := fieldRe.FindStringSubmatch(lines[i])
		if len(match) == 3 {
			lines[i] = fmt.Sprintf(`%s%s = "%s"`, match[1], fieldName, value)
			return strings.Join(lines, "\n"), nil
		}
	}
	next := append([]string{}, lines[:end]...)
	next = append(next, fmt.Sprintf(`%s = "%s"`, fieldName, value))
	next = append(next, lines[end:]...)
	return strings.Join(next, "\n"), nil
}

func removeProviderField(text string, providerName string, fieldName string) (string, error) {
	lines, start, end, err := providerSection(text, providerName)
	if err != nil {
		return "", err
	}
	fieldRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(fieldName) + `\s*=\s*"([^"]*)"\s*$`)
	for i := start + 1; i < end; i++ {
		if fieldRe.MatchString(lines[i]) {
			lines = append(lines[:i], lines[i+1:]...)
			return strings.Join(lines, "\n"), nil
		}
	}
	return strings.Join(lines, "\n"), nil
}

func providerSection(text string, providerName string) ([]string, int, int, error) {
	lines := strings.Split(text, "\n")
	header := "[model_providers." + providerName + "]"
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			start = i
			break
		}
	}
	if start == -1 {
		return nil, 0, 0, fmt.Errorf("provider section not found: %s", header)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			end = i
			break
		}
	}
	return lines, start, end, nil
}

func atomicWriteTextFile(path string, text string, mode os.FileMode) error {
	return atomicWriteBytes(path, []byte(text), mode)
}

func atomicWriteBytes(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	cleanup := func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		cleanup()
		return err
	}
	closed = true
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return fsyncDirPath(dir)
}

func fsyncDirPath(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
