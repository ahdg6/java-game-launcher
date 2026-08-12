package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCurrentConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	launcher := defaultLauncherConfig()
	launcher.Instances[0].JavaPath = filepath.Join("openjdk", "bin", javaExecutableName())
	launcher.Instances[0].JarPath = "Mindustry.jar"
	launcher.Instances[0].GameArgs = []string{"-debug", "two words"}
	server, err := launcher.CreateInstance("server", "Server")
	if err != nil {
		t.Fatal(err)
	}
	server.JarPath = "server.jar"
	server.JVMPreset = presetCustom
	server.JVMArgs = []string{}
	launcher.ActiveInstanceID = server.ID
	if err := saveLauncherConfig(path, launcher); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"version"`) {
		t.Fatalf("configuration still persists a version: %s", data)
	}
	loaded, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, launcher) {
		t.Fatalf("round trip mismatch\ngot:  %#v\nwant: %#v", loaded, launcher)
	}
}

func TestMissingConfigUsesDefaults(t *testing.T) {
	launcher, err := loadLauncherConfig(filepath.Join(t.TempDir(), configFileName))
	if err != nil {
		t.Fatal(err)
	}
	if launcher.ActiveInstanceID != defaultInstanceID || len(launcher.Instances) != 1 {
		t.Fatalf("default launcher = %#v", launcher)
	}
}

func TestObsoleteSingleInstanceConfigIsRejectedWithResetHint(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	if err := os.WriteFile(path, []byte(`{"java_path":"jdk/bin/java","jar_path":"Mindustry.jar"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLauncherConfig(path); err == nil || !strings.Contains(err.Error(), "删除此文件") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"obsolete version", `{"version":6,"active_instance_id":"main","instances":[{"id":"main","name":"Main","jvm_args":[],"game_args":[]}]}`},
		{"unknown instance field", `{"active_instance_id":"main","instances":[{"id":"main","name":"Main","mystery":true,"jvm_args":[],"game_args":[]}]}`},
		{"trailing value", `{"active_instance_id":"main","instances":[{"id":"main","name":"Main","jvm_args":[],"game_args":[]}]} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), configFileName)
			if err := os.WriteFile(path, []byte(test.raw), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadLauncherConfig(path); err == nil || !strings.Contains(err.Error(), "删除此文件") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestConfigRejectsUnknownProfileAndJVMPresetValues(t *testing.T) {
	tests := []struct{ name, field, value string }{
		{name: "profile", field: "game_profile", value: "mindusty"},
		{name: "JVM preset", field: "jvm_preset", value: "performnce"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), configFileName)
			raw := `{"active_instance_id":"main","instances":[{"id":"main","name":"Main","` + test.field + `":"` + test.value + `","jvm_args":[],"game_args":[]}]}`
			if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadLauncherConfig(path); err == nil || !strings.Contains(err.Error(), "未知") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestExplicitEmptyArgumentsSurviveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	raw := `{"active_instance_id":"empty","instances":[{"id":"empty","name":"Empty","game_profile":"mindustry","jvm_preset":"auto","jvm_args":[],"game_args":[]}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	instance := launcher.Instances[0]
	if instance.JVMArgs == nil || instance.GameArgs == nil || len(instance.JVMArgs) != 0 || len(instance.GameArgs) != 0 {
		t.Fatalf("empty arrays changed: %#v", instance)
	}
	if err := saveLauncherConfig(path, launcher); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadLauncherConfig(path)
	if err != nil || reloaded.Instances[0].JVMArgs == nil || reloaded.Instances[0].GameArgs == nil {
		t.Fatalf("reloaded = %#v, err = %v", reloaded, err)
	}
}

func TestMissingArgumentsReceiveDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	raw := `{"active_instance_id":"main","instances":[{"id":"main","name":"Main","jvm_preset":"auto"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.Instances[0].JVMArgs) == 0 || launcher.Instances[0].GameArgs == nil {
		t.Fatalf("missing arguments did not receive defaults: %#v", launcher.Instances[0])
	}
}

func TestRejectsInvalidDuplicateAndEmptyInstances(t *testing.T) {
	tests := []struct{ name, raw, want string }{
		{"empty-list", `{"active_instance_id":"","instances":[]}`, "至少需要一个实例"},
		{"invalid-id", `{"active_instance_id":"Bad","instances":[{"id":"Bad","name":"Bad","jvm_args":[],"game_args":[]}]}`, "非法"},
		{"duplicate", `{"active_instance_id":"same","instances":[{"id":"same","name":"A","jvm_args":[],"game_args":[]},{"id":"same","name":"B","jvm_args":[],"game_args":[]}]}`, "重复"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), configFileName)
			if err := os.WriteFile(path, []byte(test.raw), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadLauncherConfig(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRecoverableNamesAndActiveInstanceProduceWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	raw := `{"active_instance_id":"gone","instances":[{"id":"first","name":"","jvm_args":[],"game_args":[]}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if launcher.ActiveInstanceID != "first" || launcher.Instances[0].Name != "first" || len(launcher.Warnings) != 2 {
		t.Fatalf("launcher = %#v", launcher)
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
	clone, err := launcher.CloneInstance(defaultInstanceID, "modded", "模组")
	if err != nil {
		t.Fatal(err)
	}
	clone.JVMArgs[0] = "-Xmx9g"
	clone.GameArgs[0] = "changed"
	if launcher.InstanceByID(defaultInstanceID).JVMArgs[0] != "-Xmx2g" || launcher.InstanceByID(defaultInstanceID).GameArgs[0] != "play" {
		t.Fatal("clone shares argument backing arrays")
	}
	if err := launcher.RenameInstance("modded", "模组测试"); err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.SelectInstance("modded"); err != nil {
		t.Fatal(err)
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
		t.Fatal("deleting final instance succeeded")
	}
}
