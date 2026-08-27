package mindustry

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	ModDirectory ModType = "directory"
	ModJAR       ModType = "jar"
	ModZIP       ModType = "zip"

	// Mod metadata is tiny in practice. Limiting reads prevents a malformed
	// archive from making the launcher allocate an arbitrary amount of memory.
	maxModMetadataSize int64 = 1 << 20
)

// ModType describes how a mod is packaged on disk.
type ModType string

// Mod is one direct child of <data directory>/mods. The unexported
// modsDir value ties state-changing operations to the root that was scanned;
// callers cannot accidentally enable an unrelated path with a value assembled
// only from user input.
type Mod struct {
	Name        string
	DisplayName string
	Version     string
	Author      string
	Enabled     bool
	Path        string
	Type        ModType
	Size        int64

	modsDir string
}

// ModChange records one reversible rename.
type ModChange struct {
	Before Mod
	After  Mod
}

// ModDisablePlan can be retained by the UI while it retries a game without
// mods, then passed to RestoreMods afterward.
type ModDisablePlan struct {
	Changes []ModChange
}

type modMetadata struct {
	Name        string
	DisplayName string
	Version     string
	Author      string
}

// ScanMods scans the immediate entries under dataDir/mods. A missing
// mods directory is a valid empty installation and is not created as a side
// effect. Symlinks are skipped so metadata and later renames cannot escape the
// selected data directory.
func ScanMods(dataDir string) ([]Mod, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("扫描模组：数据目录不能为空")
	}

	dataRoot, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("扫描模组：解析数据目录：%w", err)
	}
	modsDir := filepath.Join(dataRoot, "mods")
	modsDir, err = filepath.Abs(modsDir)
	if err != nil {
		return nil, fmt.Errorf("扫描模组：解析模组目录：%w", err)
	}

	entries, err := os.ReadDir(modsDir)
	if errors.Is(err, os.ErrNotExist) {
		return []Mod{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("扫描模组：读取 %s：%w", modsDir, err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(modsDir)
	if err != nil {
		return nil, fmt.Errorf("扫描模组：解析模组目录：%w", err)
	}
	canonicalRoot, err = filepath.Abs(canonicalRoot)
	if err != nil {
		return nil, fmt.Errorf("扫描模组：解析模组目录绝对路径：%w", err)
	}

	mods := make([]Mod, 0, len(entries))
	for _, entry := range entries {
		// DirEntry.IsDir follows no symlink, but Info may. Check the type first.
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		enabled, modType, ok := classifyMod(entry.Name(), entry.IsDir())
		if !ok {
			continue
		}

		modPath := filepath.Join(canonicalRoot, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("扫描模组：读取 %s 信息：%w", entry.Name(), err)
		}
		size, err := modSize(modPath, info)
		if err != nil {
			return nil, fmt.Errorf("扫描模组：统计 %s 大小：%w", entry.Name(), err)
		}

		metadata := readModMetadata(modPath, modType)
		fallbackName := modFallbackName(entry.Name(), modType)
		if metadata.Name == "" {
			metadata.Name = fallbackName
		}
		if metadata.DisplayName == "" {
			metadata.DisplayName = metadata.Name
		}
		mods = append(mods, Mod{
			Name:        metadata.Name,
			DisplayName: metadata.DisplayName,
			Version:     metadata.Version,
			Author:      metadata.Author,
			Enabled:     enabled,
			Path:        modPath,
			Type:        modType,
			Size:        size,
			modsDir:     canonicalRoot,
		})
	}

	sort.SliceStable(mods, func(i, j int) bool {
		left := strings.ToLower(mods[i].DisplayName)
		right := strings.ToLower(mods[j].DisplayName)
		if left == right {
			return strings.ToLower(filepath.Base(mods[i].Path)) < strings.ToLower(filepath.Base(mods[j].Path))
		}
		return left < right
	})
	return mods, nil
}

func classifyMod(name string, directory bool) (bool, ModType, bool) {
	enabled := true
	base := name
	if hasSuffixFold(base, ".disabled") {
		enabled = false
		base = base[:len(base)-len(".disabled")]
	}
	if base == "" {
		return false, "", false
	}
	if directory {
		return enabled, ModDirectory, true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".jar":
		return enabled, ModJAR, true
	case ".zip":
		return enabled, ModZIP, true
	default:
		return false, "", false
	}
}

func modFallbackName(fileName string, modType ModType) string {
	name := fileName
	if hasSuffixFold(name, ".disabled") {
		name = name[:len(name)-len(".disabled")]
	}
	if modType == ModJAR || modType == ModZIP {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	return name
}

func modSize(modPath string, info fs.FileInfo) (int64, error) {
	if !info.IsDir() {
		return info.Size(), nil
	}
	var size int64
	err := filepath.WalkDir(modPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == modPath {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if entryInfo.Mode().IsRegular() {
			size += entryInfo.Size()
		}
		return nil
	})
	return size, err
}

func readModMetadata(modPath string, modType ModType) modMetadata {
	if modType == ModDirectory {
		return readDirectoryModMetadata(modPath)
	}
	return readArchiveModMetadata(modPath)
}

func readDirectoryModMetadata(modPath string) modMetadata {
	for _, name := range []string{"mod.json", "plugin.json", "mod.hjson"} {
		path := filepath.Join(modPath, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxModMetadataSize {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil {
			metadata := parseModMetadata(data, strings.HasSuffix(name, ".hjson"))
			if metadata != (modMetadata{}) {
				return metadata
			}
		}
	}
	return modMetadata{}
}

func readArchiveModMetadata(modPath string) modMetadata {
	reader, err := zip.OpenReader(modPath)
	if err != nil {
		return modMetadata{}
	}
	defer reader.Close()

	type candidate struct {
		file  *zip.File
		depth int
		rank  int
	}
	var candidates []candidate
	for _, file := range reader.File {
		clean := strings.TrimPrefix(filepath.ToSlash(file.Name), "./")
		if clean == "" || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			continue
		}
		base := strings.ToLower(filepath.Base(clean))
		rank := -1
		switch base {
		case "mod.json":
			rank = 0
		case "plugin.json":
			rank = 1
		case "mod.hjson":
			rank = 2
		}
		if rank < 0 || file.FileInfo().IsDir() || file.UncompressedSize64 > uint64(maxModMetadataSize) {
			continue
		}
		candidates = append(candidates, candidate{
			file:  file,
			depth: strings.Count(strings.Trim(clean, "/"), "/"),
			rank:  rank,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].depth != candidates[j].depth {
			return candidates[i].depth < candidates[j].depth
		}
		return candidates[i].rank < candidates[j].rank
	})
	for _, item := range candidates {
		fileReader, err := item.file.Open()
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(fileReader, maxModMetadataSize+1))
		closeErr := fileReader.Close()
		if readErr != nil || closeErr != nil || int64(len(data)) > maxModMetadataSize {
			continue
		}
		metadata := parseModMetadata(data, strings.HasSuffix(strings.ToLower(item.file.Name), ".hjson"))
		if metadata != (modMetadata{}) {
			return metadata
		}
	}
	return modMetadata{}
}

func parseModMetadata(data []byte, hjson bool) modMetadata {
	if !hjson {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			metadata := modMetadata{
				Name:        jsonMetadataString(raw, "name"),
				DisplayName: jsonMetadataString(raw, "displayName"),
				Version:     jsonMetadataString(raw, "version"),
				Author:      jsonMetadataString(raw, "author"),
			}
			if metadata != (modMetadata{}) {
				return metadata
			}
		}
	}
	return parseModHJSON(data)
}

func jsonMetadataString(values map[string]json.RawMessage, key string) string {
	for candidate, raw := range values {
		if !strings.EqualFold(candidate, key) {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			return strings.TrimSpace(value)
		}
		// Some mods publish numeric versions. Preserve those without coercing
		// arrays or objects into unhelpful JSON text.
		var number json.Number
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&number); err == nil {
			return number.String()
		}
	}
	return ""
}

var hjsonMetadataField = regexp.MustCompile(`(?i)^\s*["']?(name|displayName|version|author)["']?\s*:\s*(.*?)\s*,?\s*$`)

func parseModHJSON(data []byte) modMetadata {
	values := make(map[string]string, 4)
	for _, line := range strings.Split(string(data), "\n") {
		match := hjsonMetadataField.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if len(match) != 3 {
			continue
		}
		value := strings.TrimSpace(match[2])
		value = trimHJSONComment(value)
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), ","))
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			if value[0] == '"' {
				if unquoted, err := strconv.Unquote(value); err == nil {
					value = unquoted
				}
			} else {
				value = value[1 : len(value)-1]
			}
		}
		values[strings.ToLower(match[1])] = strings.TrimSpace(value)
	}
	return modMetadata{
		Name:        values["name"],
		DisplayName: values["displayname"],
		Version:     values["version"],
		Author:      values["author"],
	}
}

func trimHJSONComment(value string) string {
	if value == "" || value[0] == '"' || value[0] == '\'' {
		return value
	}
	comment := len(value)
	if index := strings.Index(value, "//"); index >= 0 && index < comment {
		comment = index
	}
	if index := strings.IndexByte(value, '#'); index >= 0 && index < comment {
		comment = index
	}
	return value[:comment]
}

// SetModEnabled switches a scanned mod by adding or removing exactly
// one .disabled suffix. It never removes or overwrites an existing entry.
func SetModEnabled(mod Mod, enabled bool) (Mod, error) {
	current, target, err := validateModRename(mod, enabled)
	if err != nil {
		return mod, err
	}
	if samePath(current, target) {
		mod.Enabled = enabled
		mod.Path = current
		return mod, nil
	}
	if err := os.Rename(current, target); err != nil {
		return mod, fmt.Errorf("切换模组 %s：重命名失败：%w", mod.DisplayName, err)
	}
	mod.Enabled = enabled
	mod.Path = target
	return mod, nil
}

func validateModRename(mod Mod, enabled bool) (string, string, error) {
	if strings.TrimSpace(mod.modsDir) == "" {
		return "", "", errors.New("切换模组：缺少扫描来源，请重新扫描模组目录")
	}
	root, err := filepath.Abs(mod.modsDir)
	if err != nil {
		return "", "", fmt.Errorf("切换模组：解析模组目录：%w", err)
	}
	current, err := filepath.Abs(mod.Path)
	if err != nil {
		return "", "", fmt.Errorf("切换模组：解析模组路径：%w", err)
	}
	if !samePath(filepath.Dir(current), root) || filepath.Base(current) == "." || filepath.Base(current) == string(filepath.Separator) {
		return "", "", fmt.Errorf("切换模组：路径越界，%s 不在模组目录直属范围内", mod.Path)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return "", "", fmt.Errorf("切换模组：读取模组目录：%w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("切换模组：模组目录不是安全的普通目录")
	}
	info, err := os.Lstat(current)
	if err != nil {
		return "", "", fmt.Errorf("切换模组：模组已被移动或删除：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("切换模组：拒绝操作符号链接")
	}
	actualEnabled, actualType, ok := classifyMod(filepath.Base(current), info.IsDir())
	if !ok {
		return "", "", errors.New("切换模组：路径已不再是受支持的模组")
	}
	if actualEnabled != mod.Enabled || (mod.Type != "" && actualType != mod.Type) {
		return "", "", errors.New("切换模组：磁盘状态已变化，请重新扫描")
	}
	if actualEnabled == enabled {
		return current, current, nil
	}

	target := current + ".disabled"
	if enabled {
		target = current[:len(current)-len(".disabled")]
	}
	if !samePath(filepath.Dir(target), root) || filepath.Base(target) == "" {
		return "", "", errors.New("切换模组：目标路径越界")
	}
	if _, err := os.Lstat(target); err == nil {
		return "", "", fmt.Errorf("切换模组：目标名称已存在：%s", filepath.Base(target))
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("切换模组：检查目标名称：%w", err)
	}
	return current, target, nil
}

// PlanDisableAllMods validates all requested changes without touching
// disk. Disabled mods are deliberately omitted from the plan.
func PlanDisableAllMods(mods []Mod) (ModDisablePlan, error) {
	plan := ModDisablePlan{Changes: make([]ModChange, 0, len(mods))}
	targets := make(map[string]struct{}, len(mods))
	for _, mod := range mods {
		if !mod.Enabled {
			continue
		}
		current, target, err := validateModRename(mod, false)
		if err != nil {
			return ModDisablePlan{}, fmt.Errorf("规划禁用全部模组：%w", err)
		}
		key := normalizedPathKey(target)
		if _, exists := targets[key]; exists {
			return ModDisablePlan{}, fmt.Errorf("规划禁用全部模组：重复的目标路径 %s", target)
		}
		targets[key] = struct{}{}
		after := mod
		after.Enabled = false
		after.Path = target
		before := mod
		before.Path = current
		plan.Changes = append(plan.Changes, ModChange{Before: before, After: after})
	}
	return plan, nil
}

// DisableAllMods atomically applies a disable plan as far as the
// filesystem permits. On a mid-operation failure, completed renames are
// rolled back before the error is returned.
func DisableAllMods(mods []Mod) (ModDisablePlan, error) {
	plan, err := PlanDisableAllMods(mods)
	if err != nil {
		return ModDisablePlan{}, err
	}
	if err := applyModChanges(plan.Changes, false); err != nil {
		return ModDisablePlan{}, fmt.Errorf("禁用全部模组：%w", err)
	}
	return plan, nil
}

// RestoreMods reverses a previously completed disable plan. Restore
// is also transactional: if one mod cannot be restored, earlier restores are
// re-disabled so the caller can resolve the conflict and retry the same plan.
func RestoreMods(plan ModDisablePlan) error {
	reversed := make([]ModChange, 0, len(plan.Changes))
	for index := len(plan.Changes) - 1; index >= 0; index-- {
		change := plan.Changes[index]
		reversed = append(reversed, ModChange{Before: change.After, After: change.Before})
	}
	if err := applyModChanges(reversed, true); err != nil {
		return fmt.Errorf("恢复模组：%w", err)
	}
	return nil
}

func applyModChanges(changes []ModChange, enable bool) error {
	// Preflight every item first. This catches stable conflicts before any
	// rename and narrows rollback to genuinely concurrent/filesystem failures.
	for _, change := range changes {
		current, target, err := validateModRename(change.Before, enable)
		if err != nil {
			return err
		}
		if !samePath(current, change.Before.Path) || !samePath(target, change.After.Path) {
			return errors.New("批量切换模组：计划路径与磁盘状态不一致")
		}
	}

	applied := make([]ModChange, 0, len(changes))
	for _, change := range changes {
		if _, err := SetModEnabled(change.Before, enable); err != nil {
			rollbackErr := rollbackModChanges(applied)
			if rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("回滚模组状态失败：%w", rollbackErr))
			}
			return err
		}
		applied = append(applied, change)
	}
	return nil
}

func rollbackModChanges(applied []ModChange) error {
	var rollbackErrors []error
	for index := len(applied) - 1; index >= 0; index-- {
		change := applied[index]
		if err := os.Rename(change.After.Path, change.Before.Path); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("%s：%w", change.Before.DisplayName, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func hasSuffixFold(value, suffix string) bool {
	return len(value) >= len(suffix) && strings.EqualFold(value[len(value)-len(suffix):], suffix)
}

func normalizedPathKey(path string) string {
	path = filepath.Clean(path)
	// Windows path lookup is normally case-insensitive. Lowercasing there also
	// detects two plans which would collide even if their spelling differs.
	if filepath.Separator == '\\' {
		return strings.ToLower(path)
	}
	return path
}
