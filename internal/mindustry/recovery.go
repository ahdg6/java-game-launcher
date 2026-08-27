package mindustry

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ahdg6/java-game-launcher/internal/fsutil"
)

const (
	recoveryStateFile       = "mindustry-safe-mode.json"
	recoveryStateVersion    = 1
	maxRecoveryStateSize    = 1 << 20
	recoveryStatePermission = 0o600
)

// recoveryMarker is deliberately self-contained: recovery must not
// depend on scanning whatever happens to be in the mods directory after a
// crash. Paths are absolute and are still checked against DataDirectory and
// ModsDirectory before every state-changing operation.
type recoveryMarker struct {
	Version       int              `json:"version"`
	DataDirectory string           `json:"data_directory"`
	ModsDirectory string           `json:"mods_directory"`
	OwnerPID      int              `json:"owner_pid,omitempty"`
	ProcessPID    int              `json:"process_pid,omitempty"`
	Changes       []recoveryChange `json:"changes"`
	Checksum      string           `json:"checksum"`
}

type recoveryChange struct {
	Before string  `json:"before"`
	After  string  `json:"after"`
	Type   ModType `json:"type"`
}

type recoveryChecksumPayload struct {
	Version       int              `json:"version"`
	DataDirectory string           `json:"data_directory"`
	ModsDirectory string           `json:"mods_directory"`
	OwnerPID      int              `json:"owner_pid,omitempty"`
	ProcessPID    int              `json:"process_pid,omitempty"`
	Changes       []recoveryChange `json:"changes"`
}

// BeginSafeMode starts a one-shot launch without enabled Mindustry
// mods. The recovery marker is durably published before the first rename, so
// an interrupted operation can always be completed or rolled back later.
// Calling Begin again for the same active marker is safe and completes any
// disables that may have been interrupted.
func BeginSafeMode(dataDir, stateDir string) error {
	dataRoot, modsRoot, err := recoveryRoots(dataDir)
	if err != nil {
		return err
	}
	stateRoot, exists, err := recoveryStateRoot(stateDir, true)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("启动安全模式：无法创建状态目录")
	}

	marker, pending, err := readRecoveryMarker(stateRoot)
	if err != nil {
		return err
	}
	if pending {
		if err := validateRecoveryBinding(marker, dataRoot, modsRoot); err != nil {
			return err
		}
		if pid := activeRecoveryPID(marker); pid > 0 && pid != os.Getpid() && processIsAlive(pid) {
			return fmt.Errorf("安全模式：实例正在由进程 %d 使用，拒绝修改其模组", pid)
		}
		return continueRecovery(marker)
	}

	mods, err := ScanMods(dataRoot)
	if err != nil {
		return fmt.Errorf("启动安全模式：%w", err)
	}
	plan, err := PlanDisableAllMods(mods)
	if err != nil {
		return fmt.Errorf("启动安全模式：%w", err)
	}
	marker = recoveryMarkerFromPlan(dataRoot, modsRoot, plan)
	if err := writeRecoveryMarker(stateRoot, marker); err != nil {
		return err
	}

	// Use the regular transactional mod API. If the process is killed between
	// its individual renames, the marker intentionally remains for recovery.
	applied, err := DisableAllMods(mods)
	if err != nil {
		return fmt.Errorf("启动安全模式：禁用模组：%w", err)
	}
	if !sameRecoveryPlan(marker, applied) {
		// This is an internal invariant rather than a recoverability problem; the
		// marker still describes the preflighted plan and must remain on disk.
		return errors.New("启动安全模式：禁用计划在执行前发生变化")
	}
	return nil
}

// EndSafeMode restores all mods changed by BeginSafeMode and removes the
// marker only after restoration has completed. It is idempotent.
func EndSafeMode(dataDir, stateDir string) error {
	_, err := finishRecovery(dataDir, stateDir)
	return err
}

// RecoverSafeMode restores a marker left by a prior interrupted
// launch. The bool reports whether a marker existed, including when recovery
// found a conflict and returned an error.
func RecoverSafeMode(dataDir, stateDir string) (bool, error) {
	stateRoot, exists, err := recoveryStateRoot(stateDir, false)
	if err != nil || !exists {
		return false, err
	}
	marker, pending, err := readRecoveryMarker(stateRoot)
	if err != nil || !pending {
		return pending, err
	}
	if pid := activeRecoveryPID(marker); pid > 0 && pid != os.Getpid() && processIsAlive(pid) {
		return true, fmt.Errorf("安全模式仍由运行中的游戏/启动器进程 %d 持有；本次不会提前恢复模组", pid)
	}
	return finishRecovery(dataDir, stateDir)
}

// BindSafeModeProcess binds a pending safe-mode transaction to the
// launched Java (or Flatpak bridge) process. A second launcher can then avoid
// restoring mods while the first game is still running.
func BindSafeModeProcess(dataDir, stateDir string, pid int) error {
	if pid <= 0 {
		return errors.New("安全模式：游戏进程 PID 无效")
	}
	stateRoot, exists, err := recoveryStateRoot(stateDir, false)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("安全模式：状态目录不存在")
	}
	marker, pending, err := readRecoveryMarker(stateRoot)
	if err != nil {
		return err
	}
	if !pending {
		return errors.New("安全模式：没有待绑定的恢复标记")
	}
	dataRoot, modsRoot, err := recoveryRoots(dataDir)
	if err != nil {
		return err
	}
	if err := validateRecoveryBinding(marker, dataRoot, modsRoot); err != nil {
		return err
	}
	marker.ProcessPID = pid
	marker.Checksum = recoveryChecksum(marker)
	return replaceRecoveryMarker(stateRoot, marker)
}

func activeRecoveryPID(marker recoveryMarker) int {
	if marker.ProcessPID > 0 {
		return marker.ProcessPID
	}
	return marker.OwnerPID
}

// SafeModePending reports whether stateDir contains a valid pending
// marker. A corrupt marker is reported as present together with an error so a
// UI does not silently hide a recovery problem.
func SafeModePending(stateDir string) (bool, error) {
	stateRoot, exists, err := recoveryStateRoot(stateDir, false)
	if err != nil || !exists {
		return false, err
	}
	_, pending, err := readRecoveryMarker(stateRoot)
	if err != nil {
		markerPath := filepath.Join(stateRoot, recoveryStateFile)
		if _, statErr := os.Lstat(markerPath); statErr == nil {
			return true, err
		}
		return false, err
	}
	return pending, nil
}

func finishRecovery(dataDir, stateDir string) (bool, error) {
	stateRoot, exists, err := recoveryStateRoot(stateDir, false)
	if err != nil || !exists {
		return false, err
	}
	marker, pending, err := readRecoveryMarker(stateRoot)
	if err != nil || !pending {
		return pending, err
	}

	dataRoot, modsRoot, err := recoveryRoots(dataDir)
	if err != nil {
		return true, err
	}
	if err := validateRecoveryBinding(marker, dataRoot, modsRoot); err != nil {
		return true, err
	}
	plan, err := restorableModPlan(marker)
	if err != nil {
		return true, err
	}
	if err := RestoreMods(plan); err != nil {
		return true, fmt.Errorf("结束安全模式：%w", err)
	}
	if err := removeRecoveryMarker(stateRoot); err != nil {
		return true, err
	}
	return true, nil
}

func continueRecovery(marker recoveryMarker) error {
	mods := make([]Mod, 0, len(marker.Changes))
	for _, change := range marker.Changes {
		beforeExists, _, err := inspectRecoveryChange(marker, change)
		if err != nil {
			return err
		}
		if beforeExists {
			mods = append(mods, modFromRecovery(marker, change, true))
		}
	}
	if len(mods) == 0 {
		return nil
	}
	if _, err := DisableAllMods(mods); err != nil {
		return fmt.Errorf("继续安全模式：禁用剩余模组：%w", err)
	}
	return nil
}

func restorableModPlan(marker recoveryMarker) (ModDisablePlan, error) {
	plan := ModDisablePlan{Changes: make([]ModChange, 0, len(marker.Changes))}
	// Inspect every entry before touching any of them. A stable conflict or a
	// lost file therefore never causes a partial recovery.
	for _, change := range marker.Changes {
		_, afterExists, err := inspectRecoveryChange(marker, change)
		if err != nil {
			return ModDisablePlan{}, err
		}
		if afterExists {
			plan.Changes = append(plan.Changes, ModChange{
				Before: modFromRecovery(marker, change, true),
				After:  modFromRecovery(marker, change, false),
			})
		}
	}
	return plan, nil
}

// inspectRecoveryChange returns which side exists. Exactly one side
// is expected, except that the enabled side is also the valid already-restored
// state after an interrupted End.
func inspectRecoveryChange(marker recoveryMarker, change recoveryChange) (bool, bool, error) {
	beforeInfo, beforeErr := os.Lstat(change.Before)
	afterInfo, afterErr := os.Lstat(change.After)
	beforeExists := beforeErr == nil
	afterExists := afterErr == nil
	if beforeErr != nil && !errors.Is(beforeErr, os.ErrNotExist) {
		return false, false, fmt.Errorf("安全模式：检查模组 %s：%w", filepath.Base(change.Before), beforeErr)
	}
	if afterErr != nil && !errors.Is(afterErr, os.ErrNotExist) {
		return false, false, fmt.Errorf("安全模式：检查模组 %s：%w", filepath.Base(change.After), afterErr)
	}
	if beforeExists && afterExists {
		return true, true, fmt.Errorf("安全模式：恢复冲突，%s 与 %s 同时存在", filepath.Base(change.Before), filepath.Base(change.After))
	}
	if !beforeExists && !afterExists {
		return false, false, fmt.Errorf("安全模式：恢复标记已损坏，模组 %s 的两个路径均不存在", filepath.Base(change.Before))
	}
	if beforeExists {
		if err := validateRecoveryEntry(beforeInfo, change.Before, change.Type, true); err != nil {
			return false, false, err
		}
	}
	if afterExists {
		if err := validateRecoveryEntry(afterInfo, change.After, change.Type, false); err != nil {
			return false, false, err
		}
	}
	return beforeExists, afterExists, nil
}

func validateRecoveryEntry(info os.FileInfo, path string, expectedType ModType, enabled bool) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("安全模式：拒绝操作符号链接 %s", path)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("安全模式：拒绝操作非普通模组 %s", path)
	}
	actualEnabled, actualType, ok := classifyMod(filepath.Base(path), info.IsDir())
	if !ok || actualEnabled != enabled || actualType != expectedType {
		return fmt.Errorf("安全模式：模组路径类型与恢复标记不符：%s", path)
	}
	return nil
}

func modFromRecovery(marker recoveryMarker, change recoveryChange, before bool) Mod {
	path := change.After
	enabled := false
	if before {
		path = change.Before
		enabled = true
	}
	name := modFallbackName(filepath.Base(path), change.Type)
	return Mod{
		Name:        name,
		DisplayName: name,
		Enabled:     enabled,
		Path:        path,
		Type:        change.Type,
		modsDir:     marker.ModsDirectory,
	}
}

func recoveryMarkerFromPlan(dataRoot, modsRoot string, plan ModDisablePlan) recoveryMarker {
	marker := recoveryMarker{
		Version:       recoveryStateVersion,
		DataDirectory: dataRoot,
		ModsDirectory: modsRoot,
		OwnerPID:      os.Getpid(),
		Changes:       make([]recoveryChange, 0, len(plan.Changes)),
	}
	for _, change := range plan.Changes {
		marker.Changes = append(marker.Changes, recoveryChange{
			Before: change.Before.Path,
			After:  change.After.Path,
			Type:   change.Before.Type,
		})
	}
	marker.Checksum = recoveryChecksum(marker)
	return marker
}

func sameRecoveryPlan(marker recoveryMarker, plan ModDisablePlan) bool {
	if len(marker.Changes) != len(plan.Changes) {
		return false
	}
	for index, change := range marker.Changes {
		planned := plan.Changes[index]
		if !samePath(change.Before, planned.Before.Path) || !samePath(change.After, planned.After.Path) || change.Type != planned.Before.Type {
			return false
		}
	}
	return true
}

func recoveryChecksum(marker recoveryMarker) string {
	payload := recoveryChecksumPayload{
		Version:       marker.Version,
		DataDirectory: marker.DataDirectory,
		ModsDirectory: marker.ModsDirectory,
		OwnerPID:      marker.OwnerPID,
		ProcessPID:    marker.ProcessPID,
		Changes:       marker.Changes,
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func validateRecoveryMarker(marker recoveryMarker) error {
	if marker.Version != recoveryStateVersion {
		return fmt.Errorf("安全模式：不支持的恢复标记版本 %d", marker.Version)
	}
	wantChecksum := recoveryChecksum(marker)
	if len(marker.Checksum) != len(wantChecksum) || subtle.ConstantTimeCompare([]byte(marker.Checksum), []byte(wantChecksum)) != 1 {
		return errors.New("安全模式：恢复标记校验失败，文件可能已损坏或被篡改")
	}
	if !isCleanAbsolutePath(marker.DataDirectory) || !isCleanAbsolutePath(marker.ModsDirectory) {
		return errors.New("安全模式：恢复标记包含无效的根目录")
	}
	if !samePath(marker.ModsDirectory, filepath.Join(marker.DataDirectory, "mods")) {
		return errors.New("安全模式：恢复标记中的模组目录已越界")
	}
	if marker.OwnerPID < 0 || marker.ProcessPID < 0 {
		return errors.New("安全模式：恢复标记包含无效进程 ID")
	}
	seen := make(map[string]struct{}, len(marker.Changes)*2)
	for _, change := range marker.Changes {
		if change.Type != ModDirectory && change.Type != ModJAR && change.Type != ModZIP {
			return fmt.Errorf("安全模式：恢复标记包含未知模组类型 %q", change.Type)
		}
		if !isCleanAbsolutePath(change.Before) || !isCleanAbsolutePath(change.After) ||
			!samePath(filepath.Dir(change.Before), marker.ModsDirectory) ||
			!samePath(filepath.Dir(change.After), marker.ModsDirectory) {
			return errors.New("安全模式：恢复标记包含越界的模组路径")
		}
		if change.After != change.Before+".disabled" {
			return errors.New("安全模式：恢复标记包含无效的模组重命名")
		}
		beforeEnabled, beforeType, beforeOK := classifyMod(filepath.Base(change.Before), change.Type == ModDirectory)
		afterEnabled, afterType, afterOK := classifyMod(filepath.Base(change.After), change.Type == ModDirectory)
		if !beforeOK || !afterOK || !beforeEnabled || afterEnabled || beforeType != change.Type || afterType != change.Type {
			return errors.New("安全模式：恢复标记中的模组名称或类型无效")
		}
		for _, path := range []string{change.Before, change.After} {
			key := normalizedPathKey(path)
			if _, duplicate := seen[key]; duplicate {
				return errors.New("安全模式：恢复标记包含重复路径")
			}
			seen[key] = struct{}{}
		}
	}
	return nil
}

func validateRecoveryBinding(marker recoveryMarker, dataRoot, modsRoot string) error {
	if !samePath(marker.DataDirectory, dataRoot) || !samePath(marker.ModsDirectory, modsRoot) {
		return fmt.Errorf("安全模式：恢复标记属于另一游戏实例（%s），拒绝在 %s 中恢复", marker.DataDirectory, dataRoot)
	}
	return nil
}

func isCleanAbsolutePath(path string) bool {
	return path != "" && filepath.IsAbs(path) && path == filepath.Clean(path)
}

func recoveryRoots(dataDir string) (string, string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", "", errors.New("安全模式：数据目录不能为空")
	}
	dataRoot, err := filepath.Abs(dataDir)
	if err != nil {
		return "", "", fmt.Errorf("安全模式：解析数据目录：%w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(dataRoot); resolveErr == nil {
		dataRoot, err = filepath.Abs(resolved)
		if err != nil {
			return "", "", fmt.Errorf("安全模式：解析数据目录：%w", err)
		}
	} else if !errors.Is(resolveErr, os.ErrNotExist) {
		return "", "", fmt.Errorf("安全模式：解析数据目录符号链接：%w", resolveErr)
	}
	modsRoot := filepath.Join(dataRoot, "mods")
	info, err := os.Lstat(modsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return dataRoot, modsRoot, nil
	}
	if err != nil {
		return "", "", fmt.Errorf("安全模式：读取模组目录：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", "", errors.New("安全模式：模组目录必须是安全的普通目录")
	}
	resolvedMods, err := filepath.EvalSymlinks(modsRoot)
	if err != nil {
		return "", "", fmt.Errorf("安全模式：解析模组目录：%w", err)
	}
	resolvedMods, err = filepath.Abs(resolvedMods)
	if err != nil {
		return "", "", fmt.Errorf("安全模式：解析模组目录：%w", err)
	}
	if !samePath(resolvedMods, modsRoot) {
		return "", "", errors.New("安全模式：模组目录经符号链接越界")
	}
	return dataRoot, resolvedMods, nil
}

func recoveryStateRoot(stateDir string, create bool) (string, bool, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", false, errors.New("安全模式：状态目录不能为空")
	}
	root, err := filepath.Abs(stateDir)
	if err != nil {
		return "", false, fmt.Errorf("安全模式：解析状态目录：%w", err)
	}
	if create {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", false, fmt.Errorf("安全模式：创建状态目录：%w", err)
		}
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) && !create {
		return root, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("安全模式：读取状态目录：%w", err)
	}
	if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return "", false, errors.New("安全模式：状态路径不是目录")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, fmt.Errorf("安全模式：解析状态目录：%w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", false, fmt.Errorf("安全模式：解析状态目录：%w", err)
	}
	resolvedInfo, err := os.Lstat(resolved)
	if err != nil || !resolvedInfo.IsDir() || resolvedInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("安全模式：状态目录不是安全的普通目录")
	}
	return resolved, true, nil
}

func readRecoveryMarker(stateRoot string) (recoveryMarker, bool, error) {
	path := filepath.Join(stateRoot, recoveryStateFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return recoveryMarker{}, false, nil
	}
	if err != nil {
		return recoveryMarker{}, false, fmt.Errorf("安全模式：读取恢复标记：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return recoveryMarker{}, true, errors.New("安全模式：恢复标记不是安全的普通文件")
	}
	if info.Size() > maxRecoveryStateSize {
		return recoveryMarker{}, true, errors.New("安全模式：恢复标记异常过大")
	}
	file, err := os.Open(path)
	if err != nil {
		return recoveryMarker{}, true, fmt.Errorf("安全模式：打开恢复标记：%w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxRecoveryStateSize+1))
	decoder.DisallowUnknownFields()
	var marker recoveryMarker
	if err := decoder.Decode(&marker); err != nil {
		return recoveryMarker{}, true, fmt.Errorf("安全模式：解析恢复标记：%w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return recoveryMarker{}, true, errors.New("安全模式：恢复标记包含多余数据")
	}
	if err := validateRecoveryMarker(marker); err != nil {
		return recoveryMarker{}, true, err
	}
	return marker, true, nil
}

func writeRecoveryMarker(stateRoot string, marker recoveryMarker) error {
	if err := validateRecoveryMarker(marker); err != nil {
		return err
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("安全模式：编码恢复标记：%w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(stateRoot, ".mindustry-safe-mode-*.tmp")
	if err != nil {
		return fmt.Errorf("安全模式：创建临时恢复标记：%w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(recoveryStatePermission); err != nil {
		return fmt.Errorf("安全模式：设置恢复标记权限：%w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("安全模式：写入恢复标记：%w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("安全模式：同步恢复标记：%w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("安全模式：关闭恢复标记：%w", err)
	}

	markerPath := filepath.Join(stateRoot, recoveryStateFile)
	// A hard-link publish is atomic and, unlike Rename on Unix, never replaces
	// a marker concurrently created by another launcher process.
	if err := os.Link(temporaryPath, markerPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("安全模式：已有恢复任务，请先完成恢复")
		}
		return fmt.Errorf("安全模式：原子发布恢复标记：%w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("安全模式：清理临时恢复标记：%w", err)
	}
	keepTemporary = false
	if err := syncRecoveryDirectory(stateRoot); err != nil {
		return fmt.Errorf("安全模式：同步状态目录：%w", err)
	}
	return nil
}

func replaceRecoveryMarker(stateRoot string, marker recoveryMarker) error {
	if err := validateRecoveryMarker(marker); err != nil {
		return err
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("安全模式：编码恢复标记：%w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(stateRoot, ".mindustry-safe-mode-update-*.tmp")
	if err != nil {
		return fmt.Errorf("安全模式：创建更新标记：%w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(recoveryStatePermission); err != nil {
		return fmt.Errorf("安全模式：设置更新标记权限：%w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("安全模式：写入更新标记：%w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("安全模式：同步更新标记：%w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("安全模式：关闭更新标记：%w", err)
	}
	if err := fsutil.ReplaceFile(temporaryPath, filepath.Join(stateRoot, recoveryStateFile)); err != nil {
		return fmt.Errorf("安全模式：原子更新恢复标记：%w", err)
	}
	keepTemporary = false
	return nil
}

func removeRecoveryMarker(stateRoot string) error {
	path := filepath.Join(stateRoot, recoveryStateFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("结束安全模式：清理恢复标记：%w", err)
	}
	if err := syncRecoveryDirectory(stateRoot); err != nil {
		return fmt.Errorf("结束安全模式：同步状态目录：%w", err)
	}
	return nil
}

func syncRecoveryDirectory(path string) error {
	// Windows does not permit opening a directory for fsync through os.Open.
	// The marker itself is still flushed before its atomic publication there.
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
