package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.json")
	want := defaultConfig()
	want.JavaPath = filepath.Join("openjdk-21", "bin", javaExecutableName())
	want.JarPath = "Mindustry.jar"
	want.GameArgs = []string{"-debug", "value with spaces"}
	if err := saveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestDefaultJVMArgsFavorStableFrameTimes(t *testing.T) {
	want := []string{
		"-Xms2g",
		"-Xmx2g",
		"-XX:+UseG1GC",
		"-XX:MaxGCPauseMillis=30",
		"-XX:G1ReservePercent=20",
		"-XX:+ParallelRefProcEnabled",
		"-XX:+AlwaysPreTouch",
		"-XX:+DisableExplicitGC",
		"-Dfile.encoding=UTF-8",
	}
	if got := defaultJVMArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultJVMArgs = %#v, want %#v", got, want)
	}
}

func TestLoadConfigMigratesUnmodifiedLegacyDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.json")
	legacy := `{
  "version": 1,
  "java_path": "",
  "jar_path": "",
  "jvm_args": ["-Xms512m", "-Xmx2g", "-XX:+UseG1GC", "-XX:MaxGCPauseMillis=50", "-XX:+ParallelRefProcEnabled", "-XX:+UseStringDeduplication", "-Dfile.encoding=UTF-8"],
  "game_args": []
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.JVMArgs, defaultJVMArgs()) {
		t.Fatalf("legacy defaults were not migrated: %#v", cfg.JVMArgs)
	}
}

func TestPortablePath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mindustry-launcher.json")
	javaPath := filepath.Join(dir, "jdk", "bin", javaExecutableName())
	if got, want := portablePath(cfgPath, javaPath), filepath.Join("jdk", "bin", javaExecutableName()); got != want {
		t.Fatalf("portablePath = %q, want %q", got, want)
	}
	if got := resolveConfigPath(cfgPath, portablePath(cfgPath, javaPath)); pathKey(got) != pathKey(javaPath) {
		t.Fatalf("resolveConfigPath = %q, want %q", got, javaPath)
	}
}

func TestArgsFromLines(t *testing.T) {
	got := argsFromLines("-Xmx2g\r\n\n-Dname=value with spaces\r\n")
	want := []string{"-Xmx2g", "-Dname=value with spaces"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argsFromLines = %#v, want %#v", got, want)
	}
}

func TestDefaultDataDirectoryIsRelativeToJar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "launcher.json")
	cfg := defaultConfig()
	cfg.JarPath = filepath.Join("game", "Mindustry.jar")
	want := filepath.Join(dir, "game", "game_data")
	if got := resolveDataDirectory(cfg, cfgPath); pathKey(got) != pathKey(want) {
		t.Fatalf("resolveDataDirectory = %q, want %q", got, want)
	}
	cfg.DataDirectory = ""
	if got := resolveDataDirectory(cfg, cfgPath); got != "" {
		t.Fatalf("cleared data directory resolved to %q", got)
	}
}

func TestLoadConfigMigratesDataDirectoryJVMArgument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.json")
	legacy := `{
  "version": 2,
  "java_path": "",
  "jar_path": "Mindustry.jar",
  "jvm_args": ["-Xmx2g", "-Dmindustry.data.dir=old_data"],
  "game_args": []
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != configVersion || cfg.DataDirectory != "old_data" {
		t.Fatalf("unexpected migrated config: %#v", cfg)
	}
	if want := []string{"-Xmx2g"}; !reflect.DeepEqual(cfg.JVMArgs, want) {
		t.Fatalf("data property was not removed from JVM args: %#v", cfg.JVMArgs)
	}
}

func TestClearedDataDirectoryPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher.json")
	cfg := defaultConfig()
	cfg.DataDirectory = ""
	if err := saveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DataDirectory != "" {
		t.Fatalf("cleared data directory reappeared as %q", loaded.DataDirectory)
	}
}
