package main

import (
	"archive/zip"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestParseJavaMajor(t *testing.T) {
	tests := map[string]int{
		"1.8.0_402":     8,
		"17.0.12":       17,
		"21.0.4+7-LTS":  21,
		"23-ea":         23,
		"not-a-version": 0,
	}
	for input, want := range tests {
		if got := parseJavaMajor(input); got != want {
			t.Errorf("parseJavaMajor(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestManifestValueWithContinuation(t *testing.T) {
	manifest := []byte("Manifest-Version: 1.0\r\nMain-Class: example.very.\r\n LongMain\r\n\r\n")
	if got, want := manifestValue(manifest, "Main-Class"), "example.very.LongMain"; got != want {
		t.Fatalf("manifestValue = %q, want %q", got, want)
	}
}

func TestInspectJarDeterminesRequiredJava(t *testing.T) {
	path := filepath.Join(t.TempDir(), "game.jar")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	manifest, _ := zw.Create("META-INF/MANIFEST.MF")
	_, _ = manifest.Write([]byte("Manifest-Version: 1.0\nMain-Class: game.Main\n"))
	class, _ := zw.Create("game/Main.class")
	header := []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(header[6:8], 61)
	_, _ = class.Write(header)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info := inspectJar(path)
	if info.Err != nil {
		t.Fatal(info.Err)
	}
	if info.MainClass != "game.Main" || info.RequiredJavaVersion != 17 {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestParseJavaPropertiesAndArchitecture(t *testing.T) {
	output := `Property settings:
    java.home = /opt/jdk
    java.vendor = Test Vendor
    os.arch = x86_64
    sun.arch.data.model = 64
`
	properties := parseJavaProperties(output)
	if properties["java.home"] != "/opt/jdk" || properties["sun.arch.data.model"] != "64" {
		t.Fatalf("unexpected properties: %#v", properties)
	}
	if got := normalizeArchitecture(properties["os.arch"]); got != "amd64" {
		t.Fatalf("normalizeArchitecture = %q, want amd64", got)
	}
}

func TestJavaArchitectureError(t *testing.T) {
	jar := JarInfo{NativeArchitectures: []string{"amd64", "arm64"}}
	bad := JavaCandidate{Architecture: "x86", DataModel: 32}
	if err := javaArchitectureError(bad, jar); err == nil {
		t.Fatal("expected 32-bit Java to be rejected")
	}
	good := JavaCandidate{Architecture: "x86_64", DataModel: 64}
	if err := javaArchitectureError(good, jar); err != nil {
		t.Fatalf("64-bit Java was rejected: %v", err)
	}
}
