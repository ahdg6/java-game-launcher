package app

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahdg6/java-game-launcher/internal/java"
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
		if got := java.ParseMajor(input); got != want {
			t.Errorf("ParseMajor(%q) = %d, want %d", input, got, want)
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
	properties := java.ParseProperties(output)
	if properties["java.home"] != "/opt/jdk" || properties["sun.arch.data.model"] != "64" {
		t.Fatalf("unexpected properties: %#v", properties)
	}
	if got := java.NormalizeArchitecture(properties["os.arch"]); got != "amd64" {
		t.Fatalf("NormalizeArchitecture = %q, want amd64", got)
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

func TestParseAndCheckJavaModules(t *testing.T) {
	modules := java.ParseModules("java.base@25\njava.desktop@25\njdk.unsupported@25\n")
	if missing := java.MissingModules(modules, []string{"java.desktop", "jdk.unsupported"}); len(missing) != 0 {
		t.Fatalf("unexpected missing modules: %#v", missing)
	}
	missing := java.MissingModules(modules, []string{"java.sql", "jdk.unsupported"})
	if len(missing) != 1 || missing[0] != "java.sql" {
		t.Fatalf("missingJavaModules = %#v", missing)
	}
}

func TestManifestValueUsesMainSectionAndCaseInsensitiveNames(t *testing.T) {
	manifest := []byte("Manifest-Version: 1.0\r\nmAiN-cLaSs: game.Main\r\n\r\nName: other.class\r\nMain-Class: other.Main\r\n")
	if got := manifestValue(manifest, "Main-Class"); got != "game.Main" {
		t.Fatalf("main section attribute = %q", got)
	}
	if got := manifestValue([]byte("Manifest-Version: 1.0\n\nName: other.class\nMain-Class: other.Main\n"), "Main-Class"); got != "" {
		t.Fatalf("named section leaked into main attributes: %q", got)
	}
}

func TestProbeJavaCandidatesBoundsConcurrencyAndPreservesRanking(t *testing.T) {
	raw := make([]rawJavaCandidate, 20)
	for i := range raw {
		raw[i] = rawJavaCandidate{path: fmt.Sprint(i), source: "test", rank: i}
	}
	var running, peak atomic.Int32
	probe := func(ctx context.Context, path string) (JavaCandidate, error) {
		active := running.Add(1)
		defer running.Add(-1)
		for old := peak.Load(); active > old; old = peak.Load() {
			if peak.CompareAndSwap(old, active) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		return JavaCandidate{Version: 21}, nil
	}
	results := probeJavaCandidates(context.Background(), raw, probe)
	if peak.Load() > javaProbeConcurrency || peak.Load() == 0 {
		t.Fatalf("peak concurrent probes = %d", peak.Load())
	}
	for i, candidate := range results {
		want := raw[len(raw)-1-i]
		if candidate.Path != want.path || candidate.Source != want.source || candidate.rank != want.rank || candidate.Err != nil {
			t.Fatalf("result %d = %+v, want %+v", i, candidate, want)
		}
	}
}

func TestProbeJavaCandidatesSkipsCanceledWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw := []rawJavaCandidate{{path: "first"}, {path: "second"}}
	results := probeJavaCandidates(ctx, raw, func(context.Context, string) (JavaCandidate, error) {
		t.Error("probe invoked after cancellation")
		return JavaCandidate{}, nil
	})
	for _, result := range results {
		if !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("candidate error = %v, want canceled", result.Err)
		}
	}
}
