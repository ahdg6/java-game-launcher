package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ahdg6/java-game-launcher/internal/mindustry"
)

// recoveryStateDirectory keeps recovery metadata outside game data so a data
// backup or restore cannot accidentally consume its own recovery marker.
// Stable instance IDs also prevent one instance from claiming another one's
// interrupted safe-mode transaction.
func recoveryStateDirectory(configPath, instanceID string) string {
	instanceID = strings.TrimSpace(instanceID)
	if !instanceIDPattern.MatchString(instanceID) {
		instanceID = defaultInstanceID
	}
	return filepath.Join(configDir(configPath), ".java-game-launcher", "state", instanceID)
}

func recoverInstance(cfg Config, configPath string) (bool, error) {
	stateDir := recoveryStateDirectory(configPath, cfg.InstanceID)
	pending, err := mindustry.SafeModePending(stateDir)
	if err != nil || !pending {
		return pending, err
	}
	dataDir := resolveDataDirectory(cfg, configPath)
	if dataDir == "" {
		return true, fmt.Errorf("实例 %q 有待恢复的安全模式，但数据目录为空", cfg.InstanceID)
	}
	return mindustry.RecoverSafeMode(dataDir, stateDir)
}

func recoverAllInstances(launcher LauncherConfig, configPath string) ([]string, error) {
	recoveredNames := make([]string, 0)
	recoveryErrors := make([]error, 0)
	for index := range launcher.Instances {
		instance := launcher.Instances[index]
		recovered, err := recoverInstance(instance.Config(), configPath)
		if err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("实例 %s：%w", instance.Name, err))
			continue
		}
		if recovered {
			recoveredNames = append(recoveredNames, instance.Name)
		}
	}
	return recoveredNames, errors.Join(recoveryErrors...)
}
