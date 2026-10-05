package app

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/ahdg6/java-game-launcher/internal/java"
)

type JavaCandidate struct {
	Path         string
	Source       string
	Version      int
	VersionText  string
	Architecture string
	DataModel    int
	Vendor       string
	JavaHome     string
	Err          error
	rank         int
}

type JarInfo struct {
	Path                string
	MainClass           string
	RequiredJavaVersion int
	ProfileID           string
	ProfileName         string
	NativeArchitectures []string
	Err                 error
	rank                int
}

type Environment struct {
	Java []JavaCandidate
	Jars []JarInfo
}

func discoverEnvironment(cfg Config, cfgPath string) Environment {
	return discoverEnvironmentContext(context.Background(), cfg, cfgPath)
}

func discoverEnvironmentContext(ctx context.Context, cfg Config, cfgPath string) Environment {
	roots := discoveryRoots(cfgPath)
	jars := discoverJars(cfg, cfgPath, roots)
	java := discoverJavaContext(ctx, cfg, cfgPath, roots)
	if jar, ok := selectJar(jars); ok {
		for i := range java {
			if java[i].Err == nil {
				java[i].Err = javaArchitectureError(java[i], jar)
			}
		}
	}
	return Environment{Java: java, Jars: jars}
}

func discoveryRoots(cfgPath string) []string {
	roots := []string{configDir(cfgPath)}
	if cwd, err := os.Getwd(); err == nil {
		roots = appendUniquePath(roots, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		roots = appendUniquePath(roots, filepath.Dir(exe))
	}
	return roots
}

func appendUniquePath(paths []string, path string) []string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	key := pathKey(path)
	for _, existing := range paths {
		if pathKey(existing) == key {
			return paths
		}
	}
	return append(paths, filepath.Clean(path))
}

func pathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func javaExecutableName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

type rawJavaCandidate struct {
	path, source string
	rank         int
}

func discoverJava(cfg Config, cfgPath string, roots []string) []JavaCandidate {
	return discoverJavaContext(context.Background(), cfg, cfgPath, roots)
}

func discoverJavaContext(ctx context.Context, cfg Config, cfgPath string, roots []string) []JavaCandidate {
	raw := []rawJavaCandidate{}
	seen := map[string]bool{}
	add := func(path, source string, rank int) {
		if strings.TrimSpace(path) == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		key := pathKey(path)
		if seen[key] {
			return
		}
		seen[key] = true
		raw = append(raw, rawJavaCandidate{path: filepath.Clean(path), source: source, rank: rank})
	}

	if cfg.JavaPath != "" {
		add(resolveConfigPath(cfgPath, cfg.JavaPath), "配置文件", 1000)
	}
	name := javaExecutableName()
	for rootIndex, root := range roots {
		walkLocalJava(root, 4, func(path string, depth int) {
			add(path, "启动器旁的 Java", 800-rootIndex*20-depth)
		})
	}
	// Managed macOS JREs use <runtime>/Contents/Home/bin/java, one level deeper
	// than the general nearby scan. Keep this narrowly scoped to runtimes/ so a
	// cleared explicit path can still rediscover an installed Zulu runtime.
	walkLocalJava(filepath.Join(configDir(cfgPath), "runtimes"), 5, func(path string, depth int) {
		add(path, "启动器安装的 Java", 850-depth)
	})
	if home := os.Getenv("JAVA_HOME"); home != "" {
		add(filepath.Join(home, "bin", name), "JAVA_HOME", 500)
	}
	if path, err := exec.LookPath(name); err == nil {
		add(path, "系统 PATH", 300)
	}

	return probeJavaCandidates(ctx, raw, probeJavaContext)
}

// Keep discovery responsive even when many nearby runtimes are present. Each
// worker owns a distinct result slot, preserving deterministic discovery order.
const javaProbeConcurrency = 4

func probeJavaCandidates(ctx context.Context, raw []rawJavaCandidate, probe func(context.Context, string) (JavaCandidate, error)) []JavaCandidate {
	results := make([]JavaCandidate, len(raw))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(javaProbeConcurrency, len(raw)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				item := raw[i]
				candidate := JavaCandidate{}
				err := ctx.Err()
				if err == nil {
					candidate, err = probe(ctx, item.path)
				}
				candidate.Path = item.path
				candidate.Source = item.source
				candidate.Err = err
				candidate.rank = item.rank
				results[i] = candidate
			}
		}()
	}
	for i := range raw {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool {
		if (results[i].Err == nil) != (results[j].Err == nil) {
			return results[i].Err == nil
		}
		if results[i].rank != results[j].rank {
			return results[i].rank > results[j].rank
		}
		return results[i].Version > results[j].Version
	})
	return results
}

func walkLocalJava(root string, maxDepth int, add func(path string, depth int)) {
	root = filepath.Clean(root)
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relDepth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
		if entry.IsDir() {
			if relDepth > maxDepth || (relDepth > 0 && shouldSkipDir(entry.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if relDepth <= maxDepth+1 && strings.EqualFold(entry.Name(), javaExecutableName()) {
			parent := strings.ToLower(filepath.Base(filepath.Dir(path)))
			if parent == "bin" || relDepth <= 1 {
				add(path, relDepth)
			}
		}
		return nil
	})
}

func shouldSkipDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".svn", "node_modules", "vendor", "dist", "build", "target", "logs", "saves", "mods", "maps", "screenshots":
		return true
	default:
		return false
	}
}

func probeJava(path string) (JavaCandidate, error) {
	return probeJavaContext(context.Background(), path)
}

func probeJavaContext(ctx context.Context, path string) (JavaCandidate, error) {
	runtime, err := java.ProbeContext(ctx, path)
	return JavaCandidate{
		Path:         runtime.Path,
		Version:      runtime.Version,
		VersionText:  runtime.VersionText,
		Architecture: runtime.Architecture,
		DataModel:    runtime.DataModel,
		Vendor:       runtime.Vendor,
		JavaHome:     runtime.JavaHome,
	}, err
}

func discoverJars(cfg Config, cfgPath string, roots []string) []JarInfo {
	type rawJar struct {
		path string
		rank int
	}
	raw := []rawJar{}
	seen := map[string]bool{}
	add := func(path string, rank int) {
		if strings.TrimSpace(path) == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		key := pathKey(path)
		if seen[key] {
			return
		}
		seen[key] = true
		raw = append(raw, rawJar{filepath.Clean(path), rank})
	}
	if cfg.JarPath != "" {
		add(resolveConfigPath(cfgPath, cfg.JarPath), 1000)
	}
	for rootIndex, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jar") {
				continue
			}
			rank := 500 - rootIndex*20
			name := strings.ToLower(entry.Name())
			if strings.Contains(name, "desktop") {
				rank += 20
			} else if strings.Contains(name, "game") || strings.Contains(name, "client") {
				rank += 10
			}
			add(filepath.Join(root, entry.Name()), rank)
		}
	}
	results := make([]JarInfo, 0, len(raw))
	for _, item := range raw {
		info := inspectJarForProfile(item.path, cfg.GameProfile)
		info.rank = item.rank
		if info.Err == nil {
			if cfg.GameProfile == profileAuto && info.ProfileID != profileGeneric {
				info.rank += 50
			} else if cfg.GameProfile != "" && cfg.GameProfile != profileAuto && info.ProfileID == cfg.GameProfile {
				info.rank += 100
			}
		}
		results = append(results, info)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if (results[i].Err == nil) != (results[j].Err == nil) {
			return results[i].Err == nil
		}
		return results[i].rank > results[j].rank
	})
	return results
}

func inspectJar(path string) JarInfo {
	return inspectJarForProfile(path, profileAuto)
}

func inspectJarForProfile(path, configuredProfile string) JarInfo {
	result := JarInfo{Path: path}
	zr, err := zip.OpenReader(path)
	if err != nil {
		result.Err = fmt.Errorf("打开 JAR: %w", err)
		return result
	}
	defer zr.Close()
	var manifest []byte
	for _, file := range zr.File {
		if strings.EqualFold(file.Name, "META-INF/MANIFEST.MF") {
			manifest, err = readZipFile(file, 1<<20)
			break
		}
	}
	if err != nil {
		result.Err = fmt.Errorf("读取 MANIFEST: %w", err)
		return result
	}
	result.MainClass = manifestValue(manifest, "Main-Class")
	if result.MainClass == "" {
		result.Err = fmt.Errorf("JAR 没有 Main-Class")
		return result
	}
	adapter := resolveGameAdapter(configuredProfile, result.MainClass)
	result.ProfileID = adapter.ID()
	result.ProfileName = adapter.DisplayName()
	result.NativeArchitectures = adapter.NativeArchitectures(zr.File, currentPlatform())
	className := strings.ReplaceAll(result.MainClass, ".", "/") + ".class"
	for _, file := range zr.File {
		if file.Name != className {
			continue
		}
		rc, openErr := file.Open()
		if openErr != nil {
			result.Err = fmt.Errorf("读取主类: %w", openErr)
			return result
		}
		header := make([]byte, 8)
		_, readErr := io.ReadFull(rc, header)
		_ = rc.Close()
		if readErr != nil || !bytes.Equal(header[:4], []byte{0xca, 0xfe, 0xba, 0xbe}) {
			result.Err = fmt.Errorf("主类格式无效")
			return result
		}
		classVersion := int(binary.BigEndian.Uint16(header[6:8]))
		if classVersion >= 45 {
			result.RequiredJavaVersion = classVersion - 44
		}
		return result
	}
	result.Err = fmt.Errorf("JAR 中找不到主类 %s", result.MainClass)
	return result
}

func javaArchitectureError(candidate JavaCandidate, jar JarInfo) error {
	if len(jar.NativeArchitectures) == 0 {
		return nil
	}
	arch := java.NormalizeArchitecture(candidate.Architecture)
	if arch == "" && candidate.DataModel == 32 {
		arch = "x86"
	}
	if arch == "" {
		return nil
	}
	for _, supported := range jar.NativeArchitectures {
		if arch == supported {
			return nil
		}
	}
	bits := ""
	if candidate.DataModel > 0 {
		bits = fmt.Sprintf("、%d 位", candidate.DataModel)
	}
	return fmt.Errorf("Java 架构不兼容：当前为 %s%s，游戏在 %s 只包含 %s 原生库",
		arch, bits, runtime.GOOS, strings.Join(jar.NativeArchitectures, "/"))
}

func readZipFile(file *zip.File, max int64) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max))
}

func manifestValue(data []byte, key string) string {
	// Manifest continuation lines begin with a single space.
	values := map[string]string{}
	current := ""
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		// Only the main section defines launch attributes; named entry sections
		// may contain identically named attributes with unrelated meanings.
		if line == "" {
			break
		}
		if strings.HasPrefix(line, " ") && current != "" {
			values[current] += strings.TrimPrefix(line, " ")
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			current = ""
			continue
		}
		current = strings.ToLower(strings.TrimSpace(parts[0]))
		values[current] = strings.TrimSpace(parts[1])
	}
	return values[strings.ToLower(key)]
}

func selectJava(candidates []JavaCandidate, required int) (JavaCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.Err == nil && (required == 0 || candidate.Version >= required) {
			return candidate, true
		}
	}
	return JavaCandidate{}, false
}

func selectJar(candidates []JarInfo) (JarInfo, bool) {
	for _, candidate := range candidates {
		if candidate.Err == nil {
			return candidate, true
		}
	}
	return JarInfo{}, false
}
