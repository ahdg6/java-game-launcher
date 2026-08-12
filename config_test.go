package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestPortablePath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, configFileName)
	javaPath := filepath.Join(dir, "jdk", "bin", javaExecutableName())
	if got, want := portablePath(cfgPath, javaPath), filepath.Join("jdk", "bin", javaExecutableName()); got != want {
		t.Fatalf("portablePath = %q, want %q", got, want)
	}
	if got := resolveConfigPath(cfgPath, portablePath(cfgPath, javaPath)); pathKey(got) != pathKey(javaPath) {
		t.Fatalf("resolveConfigPath = %q, want %q", got, javaPath)
	}
}

func TestDefaultDataDirectoryIsRelativeToJar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, configFileName)
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

func TestRemoveManagedDataDirectoryArgs(t *testing.T) {
	args := []string{"-Xmx2g", "-Dmindustry.data.dir=old", "-Dcustom=true", "-Dmindustry.data.dir=new"}
	want := []string{"-Xmx2g", "-Dcustom=true"}
	if got := removeManagedDataDirectoryArgs(args); !reflect.DeepEqual(got, want) {
		t.Fatalf("clean args = %#v, want %#v", got, want)
	}
}

func TestArgsFromLines(t *testing.T) {
	got := argsFromLines("-Xmx2g\r\n\n-Dname=value with spaces\r\n")
	want := []string{"-Xmx2g", "-Dname=value with spaces"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argsFromLines = %#v, want %#v", got, want)
	}
}
