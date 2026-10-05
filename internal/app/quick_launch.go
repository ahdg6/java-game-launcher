package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const autoLaunchSeconds = 3

type launchSuccessRecord struct {
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
}

// This is operational history, separate from editable launcher configuration
// and diagnostic logs. Config files sharing a directory keep separate records.
func launchSuccessPath(configPath, instanceID string) string {
	if !instanceIDPattern.MatchString(instanceID) {
		instanceID = defaultInstanceID
	}
	return filepath.Join(configPath+".state", "launch-history", instanceID+".json")
}

func launchFingerprint(cfg Config) string {
	// An absent and an explicitly empty argument list launch the same command.
	if cfg.JVMArgs == nil {
		cfg.JVMArgs = []string{}
	}
	if cfg.GameArgs == nil {
		cfg.GameArgs = []string{}
	}
	data, _ := json.Marshal(struct {
		InstanceID string `json:"instance_id"`
		Config     Config `json:"config"`
	}{cfg.InstanceID, cfg})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hasSuccessfulLaunch(configPath string, cfg Config) bool {
	data, err := os.ReadFile(launchSuccessPath(configPath, cfg.InstanceID))
	if err != nil {
		return false
	}
	var record launchSuccessRecord
	return json.Unmarshal(data, &record) == nil && record.Version == 1 && record.Fingerprint == launchFingerprint(cfg)
}

func clearSuccessfulLaunch(configPath, instanceID string) error {
	err := os.Remove(launchSuccessPath(configPath, instanceID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("清除上次自动启动记录：%w", err)
	}
	return nil
}

func recordSuccessfulLaunch(configPath string, cfg Config) error {
	path := launchSuccessPath(configPath, cfg.InstanceID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(launchSuccessRecord{Version: 1, Fingerprint: launchFingerprint(cfg)})
	if err != nil {
		return err
	}
	return atomicWriteConfig(path, append(data, '\n'), 0o644)
}

type autoLaunchState struct {
	autoLaunchChecked     bool
	autoLaunchCancelled   bool
	autoLaunchToken       uint64
	autoLaunchRemaining   int
	autoLaunchFingerprint string
}

type autoLaunchTickMsg struct{ token uint64 }

func autoLaunchTick(token uint64) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return autoLaunchTickMsg{token: token} })
}

func (m *model) cancelAutoLaunch() {
	m.autoLaunchCancelled = true
	m.autoLaunchRemaining = 0
	m.autoLaunchToken++
}

func (m *model) armAutoLaunch() tea.Cmd {
	if m.autoLaunchChecked {
		return nil
	}
	m.autoLaunchChecked = true
	if m.autoLaunchCancelled || m.statusErr || m.loading || m.launching || m.operationBusy() || m.page != pageMain || m.mode != editNone || m.picking {
		return nil
	}
	cfg := m.configForNextLaunch()
	if !hasSuccessfulLaunch(m.cfgPath, cfg) {
		return nil
	}
	m.autoLaunchFingerprint = launchFingerprint(cfg)
	m.autoLaunchRemaining = autoLaunchSeconds
	m.autoLaunchToken++
	return autoLaunchTick(m.autoLaunchToken)
}

func (m model) updateAutoLaunch(msg autoLaunchTickMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.autoLaunchToken || m.autoLaunchCancelled || m.autoLaunchRemaining == 0 {
		return m, nil
	}
	if m.loading || m.launching || m.operationBusy() || m.page != pageMain || m.mode != editNone || m.picking || m.autoLaunchFingerprint != launchFingerprint(m.configForNextLaunch()) {
		m.cancelAutoLaunch()
		return m, nil
	}
	m.autoLaunchRemaining--
	if m.autoLaunchRemaining > 0 {
		return m, autoLaunchTick(m.autoLaunchToken)
	}
	m.cancelAutoLaunch()
	return m.startLaunchPreparationFor("", true)
}
