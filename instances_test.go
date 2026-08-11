package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadLauncherConfigMigratesEveryLegacyVersion(t *testing.T) {
	for version := 1; version <= 5; version++ {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, configFileName)
			preset := ""
			if version == 5 {
				preset = presetCustom
			}
			legacy := fmt.Sprintf(`{
  "version": %d,
  "game_profile": "mindustry",
  "java_path": "runtime/bin/java",
  "jar_path": "games/Mindustry.jar",
  "working_directory": "work",
  "data_directory": "save-data",
  "jvm_preset": %q,
  "jvm_args": ["-Xmx3g", "-Dcustom=value"],
  "game_args": ["--custom", "two words"]
}`, version, preset)
			if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
				t.Fatal(err)
			}

			launcher, err := loadLauncherConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if launcher.Version != configVersion || launcher.ActiveInstanceID != defaultInstanceID || len(launcher.Instances) != 1 {
				t.Fatalf("unexpected migrated launcher: %#v", launcher)
			}
			instance := launcher.Instances[0]
			if instance.ID != defaultInstanceID || instance.Name != defaultInstanceName {
				t.Fatalf("unexpected migrated identity: %#v", instance)
			}
			if instance.JavaPath != "runtime/bin/java" || instance.JarPath != "games/Mindustry.jar" || instance.WorkingDirectory != "work" {
				t.Fatalf("legacy paths changed: %#v", instance)
			}
			if version >= 3 && instance.DataDirectory != "save-data" {
				t.Fatalf("v%d data directory = %q", version, instance.DataDirectory)
			}
			if want := []string{"-Xmx3g", "-Dcustom=value"}; !reflect.DeepEqual(instance.JVMArgs, want) {
				t.Fatalf("custom JVM args changed: got %#v want %#v", instance.JVMArgs, want)
			}
			if want := []string{"--custom", "two words"}; !reflect.DeepEqual(instance.GameArgs, want) {
				t.Fatalf("game args changed: got %#v want %#v", instance.GameArgs, want)
			}
			unchanged, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(unchanged) != legacy {
				t.Fatal("loading rewrote the legacy file")
			}
		})
	}
}

func TestLegacyDataPropertyAndPathBasesSurviveV6Migration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	legacy := `{
  "version": 2,
  "java_path": "runtime/bin/java",
  "jar_path": "games/Mindustry.jar",
  "working_directory": "work",
  "jvm_args": ["-Xmx2g", "-Dmindustry.data.dir=portable-data"],
  "game_args": []
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := launcher.Instances[0].Config()
	if got, want := resolveConfigPath(path, cfg.JavaPath), filepath.Join(dir, "runtime", "bin", "java"); pathKey(got) != pathKey(want) {
		t.Fatalf("Java path = %q, want %q", got, want)
	}
	if got, want := resolveConfigPath(path, cfg.WorkingDirectory), filepath.Join(dir, "work"); pathKey(got) != pathKey(want) {
		t.Fatalf("working path = %q, want %q", got, want)
	}
	if got, want := resolveDataDirectory(cfg, path), filepath.Join(dir, "games", "portable-data"); pathKey(got) != pathKey(want) {
		t.Fatalf("data path = %q, want %q", got, want)
	}
}

func TestV6ExplicitEmptyArgumentsSurviveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	raw := `{
  "version": 6,
  "active_instance_id": "empty",
  "instances": [{
    "id": "empty",
    "name": "Empty",
    "game_profile": "mindustry",
    "java_path": "",
    "jar_path": "",
    "data_directory": "",
    "jvm_preset": "auto",
    "jvm_args": [],
    "game_args": []
  }]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	instance := launcher.Instances[0]
	if instance.JVMArgs == nil || len(instance.JVMArgs) != 0 || instance.GameArgs == nil || len(instance.GameArgs) != 0 {
		t.Fatalf("explicit empty arrays were not preserved: %#v", instance)
	}
	if err := saveLauncherConfig(path, launcher); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"jvm_args": []`) || !strings.Contains(string(data), `"game_args": []`) {
		t.Fatalf("empty arrays were not encoded explicitly:\n%s", data)
	}
	reloaded, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Instances[0].JVMArgs == nil || reloaded.Instances[0].GameArgs == nil {
		t.Fatal("empty arrays became nil after reload")
	}
}

func TestV6MissingArgumentsReceiveDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	raw := `{
  "version": 6,
  "active_instance_id": "main",
  "instances": [{"id":"main", "name":"Main", "jvm_preset":"auto"}]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	instance := launcher.Instances[0]
	if len(instance.JVMArgs) == 0 || instance.GameArgs == nil {
		t.Fatalf("missing arrays did not receive defaults: %#v", instance)
	}
}

func TestV6RejectsInvalidOrDuplicateInstanceIDs(t *testing.T) {
	tests := []struct {
		name      string
		instances string
		wantError string
	}{
		{"empty", `[{"id":"","name":"Empty","jvm_args":[],"game_args":[]}]`, "非法"},
		{"uppercase", `[{"id":"Bad","name":"Bad","jvm_args":[],"game_args":[]}]`, "非法"},
		{"too-long", `[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"Long","jvm_args":[],"game_args":[]}]`, "非法"},
		{"duplicate", `[{"id":"same","name":"A","jvm_args":[],"game_args":[]},{"id":"same","name":"B","jvm_args":[],"game_args":[]}]`, "重复"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), configFileName)
			raw := fmt.Sprintf(`{"version":6,"active_instance_id":"same","instances":%s}`, tt.instances)
			if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := loadLauncherConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantError)
			}
		})
	}
}

func TestV6RejectsEmptyInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	if err := os.WriteFile(path, []byte(`{"version":6,"active_instance_id":"","instances":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadLauncherConfig(path)
	if err == nil || !strings.Contains(err.Error(), "至少需要一个实例") {
		t.Fatalf("error = %v", err)
	}
}

func TestInstancesWithoutV6VersionAreRejectedInsteadOfFlattened(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	raw := `{"active_instance_id":"second","instances":[{"id":"first","name":"First"},{"id":"second","name":"Second"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLauncherConfig(path); err == nil || !strings.Contains(err.Error(), "拒绝") {
		t.Fatalf("error = %v, want data-loss protection", err)
	}
}

func TestEmptyInstanceNameFallsBackWithWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	raw := `{"version":6,"active_instance_id":"main","instances":[{"id":"main","name":"","jvm_args":[],"game_args":[]}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if launcher.Instances[0].Name != "main" || len(launcher.Warnings) == 0 {
		t.Fatalf("launcher = %#v", launcher)
	}
}

func TestMissingActiveInstanceFallsBackWithWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	raw := `{"version":6,"active_instance_id":"gone","instances":[{"id":"first","name":"First","jvm_args":[],"game_args":[]}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if launcher.ActiveInstanceID != "first" || len(launcher.Warnings) != 1 {
		t.Fatalf("fallback = %#v", launcher)
	}
}

func TestLauncherConfigMultiInstanceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	launcher := LauncherConfig{
		Version:          configVersion,
		ActiveInstanceID: "server",
		Instances: []InstanceConfig{
			{
				ID: "desktop", Name: "桌面", GameProfile: profileMindustry,
				JavaPath: "jdk/bin/java", JarPath: "Mindustry.jar", DataDirectory: "game_data",
				JVMPreset: presetCustom, JVMArgs: []string{"-Xmx3g"}, GameArgs: []string{},
			},
			{
				ID: "server", Name: "服务器", GameProfile: profileMindustry,
				JavaPath: "jdk/bin/java", JarPath: "server.jar", WorkingDirectory: "server",
				DataDirectory: filepath.Join("instances", "server", "game_data"),
				JVMPreset:     presetCustom, JVMArgs: []string{}, GameArgs: []string{"host"},
			},
		},
	}
	if err := saveLauncherConfig(path, launcher); err != nil {
		t.Fatal(err)
	}
	got, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, launcher) {
		t.Fatalf("round trip mismatch\ngot:  %#v\nwant: %#v", got, launcher)
	}
}

func TestLegacySaveCreatesOneTimeExactBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	legacy := []byte(`{"version":5,"game_profile":"mindustry","java_path":"","jar_path":"Mindustry.jar","data_directory":"game_data","jvm_preset":"custom","jvm_args":["-Xmx3g"],"game_args":[]}`)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveLauncherConfig(path, launcher); err != nil {
		t.Fatal(err)
	}
	backupPath := path + ".v5.bak"
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backup, legacy) {
		t.Fatalf("backup changed\ngot:  %q\nwant: %q", backup, legacy)
	}
	if err := os.WriteFile(backupPath, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveLauncherConfig(path, launcher); err != nil {
		t.Fatal(err)
	}
	backup, err = os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "keep me" {
		t.Fatalf("existing migration backup was overwritten: %q", backup)
	}
}

func TestInstanceCRUDSelectionAndIndependentClone(t *testing.T) {
	launcher := defaultLauncherConfig()
	launcher.Instances[0].Name = "shared-name"
	launcher.Instances[0].JVMArgs = []string{"-Xmx2g"}
	launcher.Instances[0].GameArgs = []string{"play"}
	created, err := launcher.CreateInstance("server", "shared-name")
	if err != nil {
		t.Fatal(err)
	}
	if created.DataDirectory != filepath.Join("instances", "server", "game_data") {
		t.Fatalf("new data directory = %q", created.DataDirectory)
	}
	if _, err := launcher.ResolveInstance("shared-name"); err == nil || !strings.Contains(err.Error(), "不唯一") {
		t.Fatalf("ambiguous name error = %v", err)
	}
	if got, err := launcher.ResolveInstance("server"); err != nil || got.ID != "server" {
		t.Fatalf("ID resolution = %#v, %v", got, err)
	}
	clone, err := launcher.CloneInstance(defaultInstanceID, "modded", "模组")
	if err != nil {
		t.Fatal(err)
	}
	clone.JVMArgs[0] = "-Xmx9g"
	clone.GameArgs[0] = "changed"
	if launcher.InstanceByID(defaultInstanceID).JVMArgs[0] != "-Xmx2g" || launcher.InstanceByID(defaultInstanceID).GameArgs[0] != "play" {
		t.Fatal("clone shares argument backing arrays with source")
	}
	if clone.DataDirectory != filepath.Join("instances", "modded", "game_data") {
		t.Fatalf("clone data directory = %q", clone.DataDirectory)
	}
	if err := launcher.RenameInstance("modded", "模组测试"); err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.SelectInstance("modded"); err != nil || launcher.ActiveInstanceID != "modded" {
		t.Fatalf("select = %q, %v", launcher.ActiveInstanceID, err)
	}
	if err := launcher.MoveInstance("modded", -99); err != nil || launcher.Instances[0].ID != "modded" {
		t.Fatalf("move result = %#v, %v", launcher.Instances, err)
	}
	if err := launcher.DeleteInstance("modded"); err != nil || launcher.ActiveInstanceID == "modded" {
		t.Fatalf("delete result = %#v, %v", launcher, err)
	}
	if err := launcher.DeleteInstance("server"); err != nil {
		t.Fatal(err)
	}
	if err := launcher.DeleteInstance(defaultInstanceID); err == nil {
		t.Fatal("deleting the final instance unexpectedly succeeded")
	}
}

func TestCompatibilitySaveUpdatesSelectedInstanceOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	launcher := defaultLauncherConfig()
	second, err := launcher.CreateInstance("second", "Second")
	if err != nil {
		t.Fatal(err)
	}
	second.JarPath = "old.jar"
	if err := saveLauncherConfig(path, launcher); err != nil {
		t.Fatal(err)
	}
	cfg := second.Config()
	cfg.JarPath = "new.jar"
	if err := saveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceByID("second").JarPath != "new.jar" || got.InstanceByID(defaultInstanceID).JarPath != "" {
		t.Fatalf("compatibility save updated wrong instance: %#v", got)
	}
}

func TestCompatibilitySaveRejectsMissingInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	if err := saveLauncherConfig(path, defaultLauncherConfig()); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.InstanceID = "gone"
	if err := saveConfig(path, cfg); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("error = %v", err)
	}
}

func TestLauncherJSONDoesNotPersistCompatibilityFieldsInsideInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	if err := saveLauncherConfig(path, defaultLauncherConfig()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	var instances []map[string]json.RawMessage
	if err := json.Unmarshal(root["instances"], &instances); err != nil {
		t.Fatal(err)
	}
	if _, exists := instances[0]["version"]; exists {
		t.Fatalf("per-instance version was persisted: %s", data)
	}
}
