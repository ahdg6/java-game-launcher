package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	zuluMetadataEndpoint = "https://api.azul.com/metadata/v1/zulu/packages/"
	maxZuluMetadataBytes = 4 << 20
	maxZuluArchiveBytes  = int64(512 << 20)
	maxZuluExtractBytes  = int64(2 << 30)
	maxZuluArchiveFiles  = 100_000
)

type ZuluPackage struct {
	UUID         string
	Name         string
	DownloadURL  string
	SHA256       string
	JavaVersion  []int
	Size         int64
	ArchiveType  string
	OS           string
	Architecture string
	Bitness      int
}

type zuluListPackage struct {
	UUID        string `json:"package_uuid"`
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	JavaVersion []int  `json:"java_version"`
}

type zuluPackageDetail struct {
	zuluListPackage
	SHA256          string   `json:"sha256_hash"`
	Size            int64    `json:"size"`
	ArchiveType     string   `json:"archive_type"`
	OS              string   `json:"os"`
	Architecture    string   `json:"arch"`
	Bitness         int      `json:"hw_bitness"`
	SupportTerm     string   `json:"support_term"`
	ReleaseType     string   `json:"release_type"`
	ReleaseStatus   string   `json:"release_status"`
	Availability    string   `json:"availability_type"`
	JavaPackageType string   `json:"java_package_type"`
	Features        []string `json:"java_package_features"`
	JavaFXBundled   bool     `json:"javafx_bundled"`
	CRACSupported   bool     `json:"crac_supported"`
	LibCType        string   `json:"lib_c_type"`
	Certifications  []string `json:"certifications"`
}

type ZuluInstallResult struct {
	Directory string
	JavaPath  string
	Package   ZuluPackage
}

func FetchLatestZuluLTS(ctx context.Context, client *http.Client) (ZuluPackage, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	osName, architecture, bitness, archiveType, err := zuluPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return ZuluPackage{}, err
	}
	query := url.Values{
		"support_term":          {"lts"},
		"release_type":          {"PSU"},
		"os":                    {osName},
		"arch":                  {architecture},
		"hw_bitness":            {strconv.Itoa(bitness)},
		"archive_type":          {archiveType},
		"java_package_type":     {"jre"},
		"java_package_features": {"headfull"},
		"javafx_bundled":        {"false"},
		"release_status":        {"ga"},
		"availability_types":    {"CA"},
		"certifications":        {"tck"},
		"latest":                {"true"},
		"page_size":             {"100"},
	}
	packagesURL := zuluMetadataEndpoint + "?" + query.Encode()
	var listed []zuluListPackage
	if err := fetchZuluJSON(ctx, client, packagesURL, &listed); err != nil {
		return ZuluPackage{}, fmt.Errorf("查询 Azul Zulu 最新 LTS：%w", err)
	}
	listed = slices.DeleteFunc(listed, func(candidate zuluListPackage) bool {
		lowerName := strings.ToLower(candidate.Name)
		return candidate.UUID == "" || len(candidate.JavaVersion) == 0 ||
			strings.Contains(lowerName, "-crac-") ||
			(runtime.GOOS == "linux" && strings.Contains(lowerName, "_musl_"))
	})
	if len(listed) == 0 {
		return ZuluPackage{}, errors.New("Azul API 没有返回当前平台可用的 Zulu LTS JRE")
	}
	sort.SliceStable(listed, func(i, j int) bool {
		return compareJavaVersion(listed[i].JavaVersion, listed[j].JavaVersion) > 0
	})
	var rejected []error
	for _, candidate := range listed {
		var detail zuluPackageDetail
		detailURL := zuluMetadataEndpoint + url.PathEscape(candidate.UUID)
		if err := fetchZuluJSON(ctx, client, detailURL, &detail); err != nil {
			rejected = append(rejected, fmt.Errorf("%s：查询校验信息：%w", candidate.Name, err))
			continue
		}
		if detail.UUID == "" {
			detail.zuluListPackage = candidate
		}
		if err := validateZuluDetail(detail, osName, architecture, bitness, archiveType); err != nil {
			rejected = append(rejected, fmt.Errorf("%s：%w", candidate.Name, err))
			continue
		}
		return ZuluPackage{
			UUID: detail.UUID, Name: detail.Name, DownloadURL: detail.DownloadURL,
			SHA256: strings.ToLower(detail.SHA256), JavaVersion: slices.Clone(detail.JavaVersion), Size: detail.Size,
			ArchiveType: detail.ArchiveType, OS: detail.OS, Architecture: detail.Architecture, Bitness: detail.Bitness,
		}, nil
	}
	return ZuluPackage{}, fmt.Errorf("Azul API 返回的候选都未通过严格校验：%w", errors.Join(rejected...))
}

func fetchZuluJSON(ctx context.Context, client *http.Client, target string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := zuluClientForHost(client, "api.azul.com").Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", response.Status)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxZuluMetadataBytes+1))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return nil
}

func zuluClientForHost(client *http.Client, hostname string) *http.Client {
	copy := *client
	previous := copy.CheckRedirect
	copy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Hostname(), hostname) {
			return fmt.Errorf("拒绝跳转到非官方地址 %s", request.URL.Redacted())
		}
		if previous != nil {
			return previous(request, via)
		}
		if len(via) >= 10 {
			return errors.New("重定向次数过多")
		}
		return nil
	}
	return &copy
}

func validateZuluDetail(detail zuluPackageDetail, osName, architecture string, bitness int, archiveType string) error {
	if detail.DownloadURL == "" || detail.Name == "" || len(detail.JavaVersion) == 0 || detail.Size <= 0 || detail.Size > maxZuluArchiveBytes {
		return errors.New("Azul 元数据缺少下载地址、版本或合理文件大小")
	}
	download, err := url.Parse(detail.DownloadURL)
	if err != nil || download.Scheme != "https" || !strings.EqualFold(download.Hostname(), "cdn.azul.com") {
		return errors.New("Azul 元数据返回了非官方 HTTPS 下载地址")
	}
	if detail.OS != osName || detail.Architecture != architecture || detail.Bitness != bitness || detail.ArchiveType != archiveType {
		return errors.New("Zulu 包的平台信息与当前系统不匹配")
	}
	if detail.SupportTerm != "lts" || detail.ReleaseType != "PSU" || detail.ReleaseStatus != "ga" || detail.Availability != "CA" || detail.JavaPackageType != "jre" || detail.JavaFXBundled || detail.CRACSupported {
		return errors.New("Zulu 包不符合 LTS/PSU/GA/CA/普通 JRE/无 JavaFX 约束")
	}
	if !slices.Contains(detail.Certifications, "tck") || !slices.Contains(detail.Features, "headfull") {
		return errors.New("Zulu 包缺少 TCK 认证或桌面模块")
	}
	if runtime.GOOS == "linux" && detail.LibCType != "glibc" {
		return errors.New("当前 Linux 安装器只接受 glibc Zulu JRE")
	}
	if len(detail.SHA256) != sha256.Size*2 {
		return errors.New("Azul 元数据缺少有效 SHA-256")
	}
	if _, err := hex.DecodeString(detail.SHA256); err != nil {
		return errors.New("Azul 元数据中的 SHA-256 无效")
	}
	return nil
}

func zuluPlatform(goos, goarch string) (osName, architecture string, bitness int, archiveType string, err error) {
	archiveType = "tar.gz"
	bitness = 64
	switch goos {
	case "windows":
		osName, archiveType = "windows", "zip"
	case "darwin":
		osName = "macos"
	case "linux":
		osName = "linux"
	default:
		return "", "", 0, "", fmt.Errorf("当前操作系统 %s 尚不支持便携 Zulu 安装", goos)
	}
	switch goarch {
	case "amd64":
		architecture = "x86"
	case "arm64":
		architecture = "arm"
	default:
		return "", "", 0, "", fmt.Errorf("当前 CPU 架构 %s 尚不支持便携 Zulu 安装", goarch)
	}
	return osName, architecture, bitness, archiveType, nil
}

func compareJavaVersion(left, right []int) int {
	for index := range max(len(left), len(right)) {
		var a, b int
		if index < len(left) {
			a = left[index]
		}
		if index < len(right) {
			b = right[index]
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	return 0
}

func zuluVersionLabel(version []int) string {
	parts := make([]string, len(version))
	for index, number := range version {
		parts[index] = strconv.Itoa(number)
	}
	return strings.Join(parts, ".")
}

func InstallZuluPackage(ctx context.Context, client *http.Client, pkg ZuluPackage, destinationRoot string) (ZuluInstallResult, error) {
	var result ZuluInstallResult
	if err := validateZuluPackageForInstall(pkg); err != nil {
		return result, err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	}
	if err := os.MkdirAll(destinationRoot, 0o755); err != nil {
		return result, fmt.Errorf("创建运行时目录：%w", err)
	}
	archive, err := os.CreateTemp(destinationRoot, ".zulu-download-*")
	if err != nil {
		return result, fmt.Errorf("创建下载临时文件：%w", err)
	}
	archivePath := archive.Name()
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archivePath)
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pkg.DownloadURL, nil)
	if err != nil {
		return result, err
	}
	response, err := zuluClientForHost(client, "cdn.azul.com").Do(request)
	if err != nil {
		return result, fmt.Errorf("下载 Zulu JRE：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("下载 Zulu JRE：HTTP %s", response.Status)
	}
	if response.ContentLength > maxZuluArchiveBytes {
		return result, errors.New("Zulu JRE 下载大小异常")
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(response.Body, maxZuluArchiveBytes+1))
	if err != nil {
		return result, fmt.Errorf("写入 Zulu JRE：%w", err)
	}
	if written > maxZuluArchiveBytes || (pkg.Size > 0 && written != pkg.Size) {
		return result, fmt.Errorf("Zulu JRE 下载大小不符：得到 %d，预期 %d", written, pkg.Size)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, pkg.SHA256) {
		return result, fmt.Errorf("Zulu JRE SHA-256 校验失败：得到 %s，预期 %s", got, pkg.SHA256)
	}
	if err := archive.Sync(); err != nil {
		return result, err
	}
	if err := archive.Close(); err != nil {
		return result, err
	}
	installName := strings.TrimSuffix(strings.TrimSuffix(pkg.Name, ".tar.gz"), ".zip")
	finalDirectory := filepath.Join(destinationRoot, installName)
	if _, err := os.Lstat(finalDirectory); err == nil {
		return result, fmt.Errorf("目标运行时已存在：%s", finalDirectory)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	stage, err := os.MkdirTemp(destinationRoot, ".zulu-install-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(stage)
	if pkg.ArchiveType == "zip" {
		err = extractZuluZIP(archivePath, stage)
	} else {
		err = extractZuluTarGZ(archivePath, stage)
	}
	if err != nil {
		return result, err
	}
	javaPath, root, err := locateExtractedZuluJava(stage)
	if err != nil {
		return result, err
	}
	relativeJava, err := filepath.Rel(root, javaPath)
	if err != nil {
		return result, err
	}
	if _, err := probeJava(javaPath); err != nil {
		return result, fmt.Errorf("解压后的 Zulu Java 探测失败：%w", err)
	}
	if err := os.Rename(root, finalDirectory); err != nil {
		return result, fmt.Errorf("发布 Zulu JRE：%w", err)
	}
	result = ZuluInstallResult{Directory: finalDirectory, JavaPath: filepath.Join(finalDirectory, relativeJava), Package: pkg}
	return result, nil
}

func validateZuluPackageForInstall(pkg ZuluPackage) error {
	if pkg.Name == "" || pkg.DownloadURL == "" || pkg.SHA256 == "" || pkg.Size <= 0 || pkg.Size > maxZuluArchiveBytes {
		return errors.New("Zulu 安装信息不完整")
	}
	download, err := url.Parse(pkg.DownloadURL)
	if err != nil || download.Scheme != "https" || !strings.EqualFold(download.Hostname(), "cdn.azul.com") {
		return errors.New("拒绝从非 Azul 官方 HTTPS 地址安装 Java")
	}
	if pkg.ArchiveType != "zip" && pkg.ArchiveType != "tar.gz" {
		return errors.New("不支持的 Zulu 压缩格式")
	}
	if pkg.Name != filepath.Base(pkg.Name) || pkg.Name == "." || pkg.Name == ".." || strings.ContainsAny(pkg.Name, `/\\:`) {
		return errors.New("Zulu 包名包含不安全路径")
	}
	wantedSuffix := "." + pkg.ArchiveType
	if !strings.HasSuffix(strings.ToLower(pkg.Name), wantedSuffix) || len(pkg.Name) == len(wantedSuffix) {
		return errors.New("Zulu 包名与压缩格式不匹配")
	}
	return nil
}

func extractZuluZIP(archivePath, stage string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("打开 Zulu ZIP：%w", err)
	}
	defer reader.Close()
	var total int64
	if len(reader.File) > maxZuluArchiveFiles {
		return errors.New("Zulu ZIP 条目数量异常")
	}
	for _, file := range reader.File {
		relative, err := safeArchiveRelativePath(file.Name)
		if err != nil {
			return fmt.Errorf("Zulu ZIP 条目不安全：%w", err)
		}
		if relative == "" {
			continue
		}
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 || (!file.FileInfo().IsDir() && !mode.IsRegular()) {
			return fmt.Errorf("Zulu ZIP 包含不安全条目 %s", relative)
		}
		declared := int64(file.UncompressedSize64)
		if declared < 0 || declared > maxZuluExtractBytes-total {
			return errors.New("Zulu ZIP 解压大小异常")
		}
		target := filepath.Join(stage, filepath.FromSlash(relative))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, max(mode.Perm(), 0o600))
		if err != nil {
			_ = input.Close()
			return err
		}
		remaining := maxZuluExtractBytes - total
		written, copyErr := io.Copy(output, io.LimitReader(input, remaining+1))
		inputErr, outputErr := input.Close(), output.Close()
		if copyErr != nil || inputErr != nil || outputErr != nil {
			return errors.Join(copyErr, inputErr, outputErr)
		}
		if written != declared {
			return fmt.Errorf("Zulu ZIP 条目 %s 实际大小 %d 与声明的 %d 不符", relative, written, declared)
		}
		total += written
	}
	return nil
}

func extractZuluTarGZ(archivePath, stage string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("打开 Zulu tar.gz：%w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	var total int64
	entries := 0
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 Zulu tar.gz：%w", err)
		}
		entries++
		if entries > maxZuluArchiveFiles {
			return errors.New("Zulu tar 条目数量异常")
		}
		relative, err := safeArchiveRelativePath(header.Name)
		if err != nil {
			return fmt.Errorf("Zulu tar 条目不安全：%w", err)
		}
		if relative == "" {
			continue
		}
		target := filepath.Join(stage, filepath.FromSlash(relative))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxZuluExtractBytes-total {
				return errors.New("Zulu tar 解压大小异常")
			}
			total += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(output, tarReader, header.Size)
			closeErr := output.Close()
			if copyErr != nil || closeErr != nil {
				return errors.Join(copyErr, closeErr)
			}
		case tar.TypeSymlink:
			linkTarget, err := safeZuluSymlinkTarget(relative, header.Linkname)
			if err != nil {
				return fmt.Errorf("Zulu tar 符号链接 %s 不安全：%w", relative, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(filepath.FromSlash(linkTarget), target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("Zulu tar 包含不支持的特殊条目 %s", relative)
		}
	}
	return nil
}

// safeZuluSymlinkTarget accepts only relative links whose lexical destination
// stays inside the same single top-level runtime directory. It returns a link
// target relative to the link's parent, suitable for os.Symlink.
func safeZuluSymlinkTarget(linkPath, linkTarget string) (string, error) {
	if linkTarget == "" || strings.IndexByte(linkTarget, 0) >= 0 {
		return "", errors.New("链接目标为空或包含空字符")
	}
	normalized := strings.ReplaceAll(linkTarget, `\`, "/")
	if strings.HasPrefix(normalized, "/") {
		return "", errors.New("链接目标是绝对路径")
	}
	first := normalized
	if slash := strings.IndexByte(first, '/'); slash >= 0 {
		first = first[:slash]
	}
	if strings.Contains(first, ":") {
		return "", errors.New("链接目标带有卷标")
	}
	destination := path.Clean(path.Join(path.Dir(linkPath), normalized))
	linkTop, _, _ := strings.Cut(linkPath, "/")
	targetTop, _, _ := strings.Cut(destination, "/")
	if destination == "." || destination == ".." || strings.HasPrefix(destination, "../") || targetTop != linkTop {
		return "", errors.New("链接目标越过运行时顶层目录")
	}
	return path.Clean(normalized), nil
}

func locateExtractedZuluJava(stage string) (javaPath, root string, err error) {
	return locateExtractedZuluJavaForOS(stage, runtime.GOOS)
}

func locateExtractedZuluJavaForOS(stage, goos string) (javaPath, root string, err error) {
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() || entries[0].Type()&os.ModeSymlink != 0 {
		return "", "", errors.New("Zulu 压缩包必须且只能包含一个顶层运行时目录")
	}
	root = filepath.Join(stage, entries[0].Name())
	executable := "java"
	if goos == "windows" {
		executable = "java.exe"
	}
	candidates := []string{filepath.Join(root, "bin", executable)}
	if goos == "darwin" {
		candidates = append(candidates, filepath.Join(root, "Contents", "Home", "bin", executable))
	}
	for _, candidate := range candidates {
		info, statErr := os.Stat(candidate)
		if statErr == nil && info.Mode().IsRegular() {
			return candidate, root, nil
		}
	}
	return "", "", errors.New("Zulu 压缩包内没有找到 Java 可执行文件")
}
