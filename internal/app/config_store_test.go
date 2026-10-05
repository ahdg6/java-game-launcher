package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveConfigDoesNotNormalizeCaller(t *testing.T) {
	for _, scenario := range []string{"success", "invalid instance", "write failure"} {
		t.Run(scenario, func(t *testing.T) {
			launcher := LauncherConfig{
				ActiveInstanceID: "missing",
				Instances: []InstanceConfig{
					{ID: "main", JVMArgs: []string{"-Dmindustry.data.dir=old"}},
				},
				Warnings: []string{"original warning"},
			}
			path := filepath.Join(t.TempDir(), configFileName)
			switch scenario {
			case "invalid instance":
				launcher.Instances = append(launcher.Instances, InstanceConfig{ID: "INVALID"})
			case "write failure":
				// Replacing a directory with a config file must fail on every OS.
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			before, err := json.Marshal(launcher)
			if err != nil {
				t.Fatal(err)
			}
			err = saveLauncherConfig(path, launcher)
			if (err == nil) != (scenario == "success") {
				t.Fatalf("save error = %v", err)
			}
			after, err := json.Marshal(launcher)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) || len(launcher.Warnings) != 1 || launcher.Warnings[0] != "original warning" {
				t.Fatalf("saving changed caller state:\nbefore: %s\nafter: %s\nwarnings: %v", before, after, launcher.Warnings)
			}
			if scenario == "success" {
				loaded, err := loadLauncherConfig(path)
				if err != nil {
					t.Fatal(err)
				}
				if loaded.ActiveInstanceID != "main" || loaded.Instances[0].Name != "main" || loaded.Instances[0].GameProfile != profileAuto {
					t.Fatalf("saved config was not normalized: %#v", loaded)
				}
			}
		})
	}
}
