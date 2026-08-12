package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestZuluPlatformMappings(t *testing.T) {
	tests := []struct{ os, arch, wantOS, wantArch, archive string }{
		{"windows", "amd64", "windows", "x86", "zip"},
		{"darwin", "arm64", "macos", "arm", "tar.gz"},
		{"linux", "amd64", "linux", "x86", "tar.gz"},
	}
	for _, test := range tests {
		osName, arch, bits, archive, err := zuluPlatform(test.os, test.arch)
		if err != nil || osName != test.wantOS || arch != test.wantArch || bits != 64 || archive != test.archive {
			t.Fatalf("platform %s/%s = %s/%s/%d/%s, %v", test.os, test.arch, osName, arch, bits, archive, err)
		}
	}
	if _, _, _, _, err := zuluPlatform("plan9", "amd64"); err == nil {
		t.Fatal("unsupported OS was accepted")
	}
}

func TestFetchLatestZuluLTSPicksHighestNormalPackageAndValidatesDetail(t *testing.T) {
	osName, arch, bits, archiveType, err := zuluPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/metadata/v1/zulu/packages/" {
			_ = json.NewEncoder(writer).Encode([]zuluListPackage{
				{UUID: "older", Name: "zulu21-jre", JavaVersion: []int{21, 0, 12}},
				{UUID: "crac", Name: "zulu25-crac-jre", JavaVersion: []int{25, 0, 5}},
				{UUID: "non-psu", Name: "zulu25-jre-newer", JavaVersion: []int{25, 0, 5}},
				{UUID: "latest", Name: "zulu25-jre", JavaVersion: []int{25, 0, 4}},
			})
			return
		}
		uuid := strings.TrimPrefix(request.URL.Path, "/metadata/v1/zulu/packages/")
		if uuid != "non-psu" && uuid != "latest" {
			http.NotFound(writer, request)
			return
		}
		detail := zuluPackageDetail{
			zuluListPackage: zuluListPackage{UUID: "latest", Name: "zulu25-jre." + archiveType, DownloadURL: "https://cdn.azul.com/zulu/bin/test", JavaVersion: []int{25, 0, 4}},
			SHA256:          strings.Repeat("a", 64), Size: 1024, ArchiveType: archiveType, OS: osName,
			Architecture: arch, Bitness: bits, SupportTerm: "lts", ReleaseType: "PSU", ReleaseStatus: "ga", Availability: "CA",
			JavaPackageType: "jre", Features: []string{"headfull"}, Certifications: []string{"tck"},
		}
		if uuid == "non-psu" {
			detail.UUID = uuid
			detail.Name = "zulu25-jre-newer." + archiveType
			detail.JavaVersion = []int{25, 0, 5}
			detail.ReleaseType = "CPU"
		}
		if runtime.GOOS == "linux" {
			detail.LibCType = "glibc"
		}
		_ = json.NewEncoder(writer).Encode(detail)
	}))
	defer server.Close()
	// FetchLatestZuluLTS intentionally uses a constant production endpoint.
	// Exercise its parsing/selection with a transport that rewrites only the
	// destination host while keeping the requested paths and official URL checks.
	client := server.Client()
	client.Transport = rewriteTransport{base: server.URL, next: client.Transport}
	pkg, err := FetchLatestZuluLTS(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.UUID != "latest" || zuluVersionLabel(pkg.JavaVersion) != "25.0.4" {
		t.Fatalf("package = %#v", pkg)
	}
}

type rewriteTransport struct {
	base string
	next http.RoundTripper
}

func (transport rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(transport.base, "http://")
	return transport.next.RoundTrip(clone)
}

func TestInstallZuluPackageRejectsChecksumMismatchBeforeExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("not an archive"))
	}))
	defer server.Close()
	pkg := ZuluPackage{Name: "zulu-test.tar.gz", DownloadURL: "https://cdn.azul.com/zulu/bin/test", SHA256: strings.Repeat("0", 64), Size: int64(len("not an archive")), ArchiveType: "tar.gz"}
	client := server.Client()
	client.Transport = rewriteTransport{base: server.URL, next: client.Transport}
	root := t.TempDir()
	if _, err := InstallZuluPackage(context.Background(), client, pkg, root); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("checksum error = %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("install leftovers = %v, err = %v", entries, err)
	}
}

func TestExtractZuluTarRejectsTraversalAndExtractsRegularRuntime(t *testing.T) {
	malicious := filepath.Join(t.TempDir(), "bad.tar.gz")
	writeZuluTarFixture(t, malicious, map[string]string{"../escape": "bad"})
	if err := extractZuluTarGZ(malicious, t.TempDir()); err == nil || !strings.Contains(err.Error(), "不安全") {
		t.Fatalf("traversal error = %v", err)
	}

	archive := filepath.Join(t.TempDir(), "good.tar.gz")
	javaRelative := filepath.ToSlash(filepath.Join("zulu-test", "bin", javaExecutableName()))
	writeZuluTarFixture(t, archive, map[string]string{javaRelative: "java", "zulu-test/lib/modules": "modules"})
	stage := t.TempDir()
	if err := extractZuluTarGZ(archive, stage); err != nil {
		t.Fatal(err)
	}
	javaPath, root, err := locateExtractedZuluJava(stage)
	if err != nil || filepath.Base(root) != "zulu-test" || filepath.Base(javaPath) != javaExecutableName() {
		t.Fatalf("located java=%q root=%q err=%v", javaPath, root, err)
	}
}

func TestExtractZuluTarSupportsContainedSymlinkAndRejectsOversizedEntry(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "links.tar.gz")
	output, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, header := range []*tar.Header{
		{Name: "zulu/legal/java.base/LICENSE", Mode: 0o644, Size: 7, Typeflag: tar.TypeReg},
		{Name: "zulu/legal/java.desktop/LICENSE", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "../java.base/LICENSE"},
	} {
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			_, _ = tarWriter.Write([]byte("license"))
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	if err := extractZuluTarGZ(archive, stage); err != nil {
		t.Fatal(err)
	}
	linked, err := os.ReadFile(filepath.Join(stage, "zulu", "legal", "java.desktop", "LICENSE"))
	if err != nil || string(linked) != "license" {
		t.Fatalf("contained symlink content = %q, err = %v", linked, err)
	}

	oversized := filepath.Join(t.TempDir(), "oversized.tar.gz")
	writeZuluTarHeaders(t, oversized, []*tar.Header{{
		Name: "zulu/huge", Mode: 0o644, Size: maxZuluExtractBytes + 1, Typeflag: tar.TypeReg,
	}})
	if err := extractZuluTarGZ(oversized, t.TempDir()); err == nil || !strings.Contains(err.Error(), "大小异常") {
		t.Fatalf("oversized tar error = %v", err)
	}
}

func writeZuluTarHeaders(t *testing.T, name string, headers []*tar.Header) {
	t.Helper()
	output, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, header := range headers {
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	// Closing an intentionally truncated fixture returns an error because the
	// declared payload was not written. The extractor rejects the header before
	// attempting to consume that payload, which is the behavior under test.
	_ = tarWriter.Close()
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeZuluSymlinkTargetStaysInsideRuntimeRoot(t *testing.T) {
	tests := []struct {
		name, link, target string
		want               string
		valid              bool
	}{
		{"official legal link", "zulu/legal/java.desktop/LICENSE", "../java.base/LICENSE", "../java.base/LICENSE", true},
		{"nested link", "zulu/lib/current", "server/libjvm.so", "server/libjvm.so", true},
		{"parent escape", "zulu/legal/LICENSE", "../../../outside", "", false},
		{"absolute", "zulu/lib/current", "/etc/passwd", "", false},
		{"windows separators", "zulu/legal/LICENSE", `..\..\outside`, "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := safeZuluSymlinkTarget(test.link, test.target)
			if (err == nil) != test.valid || got != test.want {
				t.Fatalf("target = %q, err = %v", got, err)
			}
		})
	}
}

func writeZuluTarFixture(t *testing.T, name string, files map[string]string) {
	t.Helper()
	output, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(gzipWriter)
	for path, content := range files {
		header := &tar.Header{Name: path, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateZuluInstallRequiresOfficialURL(t *testing.T) {
	pkg := ZuluPackage{Name: "x.zip", DownloadURL: "https://example.com/x.zip", SHA256: hex.EncodeToString(make([]byte, sha256.Size)), Size: 1, ArchiveType: "zip"}
	if err := validateZuluPackageForInstall(pkg); err == nil || !strings.Contains(err.Error(), "官方") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestValidateZuluInstallRejectsUnsafePackageName(t *testing.T) {
	pkg := ZuluPackage{Name: "../outside.tar.gz", DownloadURL: "https://cdn.azul.com/zulu/bin/test", SHA256: hex.EncodeToString(make([]byte, sha256.Size)), Size: 1, ArchiveType: "tar.gz"}
	if err := validateZuluPackageForInstall(pkg); err == nil || !strings.Contains(err.Error(), "包名") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestZuluClientRejectsRedirectAwayFromOfficialHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://example.com/untrusted", http.StatusFound)
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = rewriteTransport{base: server.URL, next: client.Transport}
	request, err := http.NewRequest(http.MethodGet, "https://cdn.azul.com/zulu/bin/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zuluClientForHost(client, "cdn.azul.com").Do(request); err == nil || !strings.Contains(err.Error(), "非官方") {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestExtractCurrentOfficialZuluArchiveWhenAvailable(t *testing.T) {
	archive := os.Getenv("ZULU_TEST_ARCHIVE")
	if archive == "" {
		t.Skip("set ZULU_TEST_ARCHIVE to an official current-platform archive for integration coverage")
	}
	stage := t.TempDir()
	var err error
	if strings.HasSuffix(strings.ToLower(archive), ".zip") {
		err = extractZuluZIP(archive, stage)
	} else {
		err = extractZuluTarGZ(archive, stage)
	}
	if err != nil {
		t.Fatal(err)
	}
	archiveOS := runtime.GOOS
	if selected := os.Getenv("ZULU_TEST_ARCHIVE_OS"); selected != "" {
		archiveOS = selected
	}
	javaPath, _, err := locateExtractedZuluJavaForOS(stage, archiveOS)
	if err != nil {
		t.Fatal(err)
	}
	if archiveOS == runtime.GOOS {
		if _, err := probeJava(javaPath); err != nil {
			t.Fatalf("official extracted Java probe: %v", err)
		}
	} else if info, err := os.Stat(javaPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("official extracted Java probe: %v", err)
	}
}
