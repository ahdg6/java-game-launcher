package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const backupFilePrefix = "mindustry-data-"

// BackupSkipped records an item that was deliberately left out of a backup.
// Symlinks are always skipped: following them could unexpectedly copy files
// from outside the selected data directory.
type BackupSkipped struct {
	Path   string
	Reason string
}

type BackupResult struct {
	Path           string
	FileCount      int
	DirectoryCount int
	SourceBytes    int64
	ArchiveBytes   int64
	CreatedAt      time.Time
	Skipped        []BackupSkipped
}

type BackupInfo struct {
	Path    string
	Name    string
	Size    int64
	ModTime time.Time
}

type RestoreOptions struct {
	// Overwrite permits replacing existing files. It remains false by default,
	// so RestoreDataBackup never silently destroys user data.
	Overwrite bool
}

type RestoreResult struct {
	Destination    string
	FileCount      int
	DirectoryCount int
	BytesWritten   int64
}

// CreateDataBackup creates a ZIP archive from dataDir. destination may be a
// backup directory (a timestamped name is generated) or an explicit .zip
// filename. The archive is completely written beside its final path before it
// is atomically published.
func CreateDataBackup(dataDir, destination string) (BackupResult, error) {
	var result BackupResult
	if strings.TrimSpace(dataDir) == "" {
		return result, errors.New("创建备份：数据目录不能为空")
	}
	if strings.TrimSpace(destination) == "" {
		return result, errors.New("创建备份：备份位置不能为空")
	}

	root, err := filepath.Abs(dataDir)
	if err != nil {
		return result, fmt.Errorf("创建备份：解析数据目录：%w", err)
	}
	// Selecting a symlink as the data directory is explicit and useful, but
	// links encountered below this root are never followed.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, fmt.Errorf("创建备份：无法访问数据目录 %s：%w", dataDir, err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return result, fmt.Errorf("创建备份：读取数据目录：%w", err)
	}
	if !rootInfo.IsDir() {
		return result, fmt.Errorf("创建备份：%s 不是目录", dataDir)
	}

	finalPath, err := chooseBackupPath(destination)
	if err != nil {
		return result, err
	}
	if err := rejectSymlinkAncestors(filepath.Dir(finalPath)); err != nil {
		return result, fmt.Errorf("创建备份：备份位置不安全：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return result, fmt.Errorf("创建备份：创建备份目录：%w", err)
	}
	if _, err := os.Lstat(finalPath); err == nil {
		return result, fmt.Errorf("创建备份：目标文件已存在：%s", finalPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("创建备份：检查目标文件：%w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(finalPath), ".backup-*.zip.tmp")
	if err != nil {
		return result, fmt.Errorf("创建备份：创建临时文件：%w", err)
	}
	tmpPath := tmp.Name()
	keepTemp := false
	defer func() {
		_ = tmp.Close()
		if !keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	zw := zip.NewWriter(tmp)
	backupDirRel := containedRelativePath(root, filepath.Dir(finalPath))
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("访问 %s：%w", filePath, walkErr)
		}
		if filePath == root {
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return fmt.Errorf("计算相对路径 %s：%w", filePath, err)
		}
		rel = filepath.Clean(rel)

		// A backup folder under the game data directory must not recursively
		// include itself. When the ZIP is directly in root, only its temporary
		// and final files need to be ignored.
		if backupDirRel != "" && backupDirRel != "." && rel == backupDirRel && entry.IsDir() {
			result.Skipped = append(result.Skipped, BackupSkipped{Path: filepath.ToSlash(rel), Reason: "备份输出目录"})
			return filepath.SkipDir
		}
		absPath, absErr := filepath.Abs(filePath)
		if absErr == nil && (samePath(absPath, tmpPath) || samePath(absPath, finalPath)) {
			return nil
		}

		if entry.Type()&os.ModeSymlink != 0 {
			result.Skipped = append(result.Skipped, BackupSkipped{Path: filepath.ToSlash(rel), Reason: "符号链接"})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && isRegenerableTopLevelDirectory(rel) {
			result.Skipped = append(result.Skipped, BackupSkipped{Path: filepath.ToSlash(rel), Reason: "可再生缓存或临时目录"})
			return filepath.SkipDir
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("读取 %s 的信息：%w", rel, err)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			result.Skipped = append(result.Skipped, BackupSkipped{Path: filepath.ToSlash(rel), Reason: "不支持的特殊文件"})
			return nil
		}
		if err := addPathToBackup(zw, filePath, rel, info, &result); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		_ = zw.Close()
		return BackupResult{}, fmt.Errorf("创建备份：打包数据：%w", err)
	}
	if err := zw.Close(); err != nil {
		return BackupResult{}, fmt.Errorf("创建备份：完成 ZIP：%w", err)
	}
	if err := tmp.Sync(); err != nil {
		return BackupResult{}, fmt.Errorf("创建备份：同步临时文件：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return BackupResult{}, fmt.Errorf("创建备份：关闭临时文件：%w", err)
	}
	if err := publishBackupFile(tmpPath, finalPath); err != nil {
		return BackupResult{}, err
	}
	keepTemp = true // publishBackupFile has moved or removed the temporary path.

	archiveInfo, err := os.Stat(finalPath)
	if err != nil {
		return BackupResult{}, fmt.Errorf("创建备份：读取成品信息：%w", err)
	}
	result.Path = finalPath
	result.ArchiveBytes = archiveInfo.Size()
	result.CreatedAt = archiveInfo.ModTime()
	return result, nil
}

func chooseBackupPath(destination string) (string, error) {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("创建备份：解析备份位置：%w", err)
	}
	info, statErr := os.Stat(destination)
	if statErr == nil && info.IsDir() {
		return nextGeneratedBackupPath(destination), nil
	}
	if statErr == nil {
		return destination, nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("创建备份：读取备份位置：%w", statErr)
	}
	if strings.EqualFold(filepath.Ext(destination), ".zip") {
		return destination, nil
	}
	return nextGeneratedBackupPath(destination), nil
}

func nextGeneratedBackupPath(directory string) string {
	stamp := time.Now().Format("20060102-150405.000")
	for number := 0; ; number++ {
		name := backupFilePrefix + stamp + ".zip"
		if number > 0 {
			name = fmt.Sprintf("%s%s-%d.zip", backupFilePrefix, stamp, number)
		}
		candidate := filepath.Join(directory, name)
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

func isRegenerableTopLevelDirectory(rel string) bool {
	if filepath.Dir(rel) != "." {
		return false
	}
	switch strings.ToLower(filepath.Base(rel)) {
	case "cache", ".cache", "tmp", "temp":
		return true
	default:
		return false
	}
}

func addPathToBackup(zw *zip.Writer, filePath, rel string, info fs.FileInfo, result *BackupResult) error {
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return fmt.Errorf("生成 %s 的 ZIP 信息：%w", rel, err)
	}
	header.Name = filepath.ToSlash(rel)
	header.SetMode(info.Mode())
	if info.IsDir() {
		header.Name += "/"
		header.Method = zip.Store
		if _, err := zw.CreateHeader(header); err != nil {
			return fmt.Errorf("写入目录 %s：%w", rel, err)
		}
		result.DirectoryCount++
		return nil
	}
	header.Method = zip.Deflate
	w, err := zw.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("写入文件头 %s：%w", rel, err)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("打开文件 %s：%w", rel, err)
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil {
		return fmt.Errorf("确认文件 %s：%w", rel, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return fmt.Errorf("文件 %s 在备份过程中被替换或变为符号链接", rel)
	}
	written, err := io.Copy(w, f)
	if err != nil {
		return fmt.Errorf("读取文件 %s：%w", rel, err)
	}
	result.FileCount++
	result.SourceBytes += written
	return nil
}

func publishBackupFile(tmpPath, finalPath string) error {
	// A hard link makes publication atomic and refuses to replace a file that
	// appeared after the earlier existence check. Both paths share a directory.
	if err := os.Link(tmpPath, finalPath); err == nil {
		if err := os.Remove(tmpPath); err != nil {
			_ = os.Remove(finalPath)
			return fmt.Errorf("创建备份：清理临时文件：%w", err)
		}
		return nil
	} else if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("创建备份：目标文件已存在：%s", finalPath)
	}
	// Some filesystems do not support hard links. Rename is still atomic; the
	// second existence check keeps the non-overwrite behavior for normal use.
	if _, err := os.Lstat(finalPath); err == nil {
		return fmt.Errorf("创建备份：目标文件已存在：%s", finalPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("创建备份：检查目标文件：%w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("创建备份：发布 ZIP：%w", err)
	}
	return nil
}

// ListBackups returns regular .zip files in directory, newest first. A missing
// directory is treated as an empty backup collection.
func ListBackups(directory string) ([]BackupInfo, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []BackupInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("列出备份：读取目录：%w", err)
	}
	backups := make([]BackupInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".zip") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("列出备份：读取 %s：%w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		backups = append(backups, BackupInfo{
			Path: filepath.Join(directory, entry.Name()), Name: entry.Name(), Size: info.Size(), ModTime: info.ModTime(),
		})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].ModTime.Equal(backups[j].ModTime) {
			return backups[i].Name > backups[j].Name
		}
		return backups[i].ModTime.After(backups[j].ModTime)
	})
	return backups, nil
}

type restoreEntry struct {
	file  *zip.File
	rel   string
	isDir bool
}

// BackupPreview is a validated, read-only summary used by the TUI before a
// restore. Inspecting uses the same path and entry validation as restoration,
// so an unsafe archive is rejected before the confirmation prompt is shown.
type BackupPreview struct {
	Path              string
	FileCount         int
	DirectoryCount    int
	UncompressedBytes uint64
	TopLevel          []string
}

func InspectDataBackup(zipPath string) (BackupPreview, error) {
	preview := BackupPreview{Path: zipPath}
	if strings.TrimSpace(zipPath) == "" {
		return preview, errors.New("检查备份：ZIP 路径不能为空")
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return preview, fmt.Errorf("检查备份：打开 ZIP：%w", err)
	}
	defer zr.Close()
	entries, err := validateRestoreEntries(zr.File)
	if err != nil {
		return preview, err
	}
	topLevel := make(map[string]struct{})
	for _, entry := range entries {
		if entry.isDir {
			preview.DirectoryCount++
		} else {
			preview.FileCount++
			preview.UncompressedBytes += entry.file.UncompressedSize64
		}
		name := entry.rel
		if slash := strings.IndexByte(name, '/'); slash >= 0 {
			name = name[:slash]
		}
		if name != "" {
			topLevel[name] = struct{}{}
		}
	}
	preview.TopLevel = make([]string, 0, len(topLevel))
	for name := range topLevel {
		preview.TopLevel = append(preview.TopLevel, name)
	}
	sort.Strings(preview.TopLevel)
	return preview, nil
}

// RestoreDataBackup validates and stages the entire ZIP before copying it into
// destination. With no options, or a zero RestoreOptions, existing files cause
// an error. Pass RestoreOptions{Overwrite: true} to replace existing files.
func RestoreDataBackup(zipPath, destination string, options ...RestoreOptions) (RestoreResult, error) {
	var result RestoreResult
	if strings.TrimSpace(zipPath) == "" {
		return result, errors.New("恢复备份：ZIP 路径不能为空")
	}
	if strings.TrimSpace(destination) == "" {
		return result, errors.New("恢复备份：目标目录不能为空")
	}
	if len(options) > 1 {
		return result, errors.New("恢复备份：只能提供一组恢复选项")
	}
	var option RestoreOptions
	if len(options) == 1 {
		option = options[0]
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return result, fmt.Errorf("恢复备份：打开 ZIP：%w", err)
	}
	defer zr.Close()
	entries, err := validateRestoreEntries(zr.File)
	if err != nil {
		return result, err
	}

	destination, err = filepath.Abs(destination)
	if err != nil {
		return result, fmt.Errorf("恢复备份：解析目标目录：%w", err)
	}
	if err := rejectSymlinkAncestors(destination); err != nil {
		return result, fmt.Errorf("恢复备份：目标目录不安全：%w", err)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return result, fmt.Errorf("恢复备份：创建目标的上级目录：%w", err)
	}
	stage, err := os.MkdirTemp(parent, ".restore-*")
	if err != nil {
		return result, fmt.Errorf("恢复备份：创建临时目录：%w", err)
	}
	defer os.RemoveAll(stage)

	result.Destination = destination
	if err := extractRestoreStage(entries, stage, &result); err != nil {
		return RestoreResult{}, err
	}
	destinationExists, err := preflightRestoreDestination(destination, entries, option.Overwrite)
	if err != nil {
		return RestoreResult{}, err
	}
	if !destinationExists {
		if err := os.Rename(stage, destination); err != nil {
			return RestoreResult{}, fmt.Errorf("恢复备份：发布恢复目录：%w", err)
		}
		return result, nil
	}
	if err := mergeRestoreStage(stage, destination, entries, option.Overwrite); err != nil {
		return RestoreResult{}, err
	}
	return result, nil
}

func validateRestoreEntries(files []*zip.File) ([]restoreEntry, error) {
	entries := make([]restoreEntry, 0, len(files))
	kinds := make(map[string]bool, len(files)) // true means directory.
	for _, file := range files {
		rel, err := safeArchiveRelativePath(file.Name)
		if err != nil {
			return nil, fmt.Errorf("恢复备份：ZIP 条目 %q 不安全：%w", file.Name, err)
		}
		if rel == "" {
			continue
		}
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("恢复备份：ZIP 条目 %q 是符号链接，已拒绝", file.Name)
		}
		isDir := file.FileInfo().IsDir()
		if !isDir && !mode.IsRegular() {
			return nil, fmt.Errorf("恢复备份：ZIP 条目 %q 不是普通文件", file.Name)
		}
		if _, duplicate := kinds[rel]; duplicate {
			return nil, fmt.Errorf("恢复备份：ZIP 中存在重复路径 %q", rel)
		}
		kinds[rel] = isDir
		entries = append(entries, restoreEntry{file: file, rel: rel, isDir: isDir})
	}
	for rel := range kinds {
		for parent := path.Dir(rel); parent != "." && parent != "/"; parent = path.Dir(parent) {
			if isDir, ok := kinds[parent]; ok && !isDir {
				return nil, fmt.Errorf("恢复备份：ZIP 中的文件 %q 被当作目录使用", parent)
			}
		}
	}
	return entries, nil
}

func safeArchiveRelativePath(name string) (string, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", errors.New("路径包含空字符")
	}
	normalized := strings.ReplaceAll(name, `\`, "/")
	if normalized == "" {
		return "", errors.New("路径为空")
	}
	if strings.HasPrefix(normalized, "/") {
		return "", errors.New("不允许绝对路径")
	}
	first := normalized
	if slash := strings.IndexByte(first, '/'); slash >= 0 {
		first = first[:slash]
	}
	if strings.Contains(first, ":") {
		return "", errors.New("不允许带卷标的路径")
	}
	clean := path.Clean(normalized)
	if clean == "." {
		if strings.HasSuffix(normalized, "/") {
			return "", nil
		}
		return "", errors.New("路径没有文件名")
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("路径会越过目标目录")
	}
	return clean, nil
}

func extractRestoreStage(entries []restoreEntry, stage string, result *RestoreResult) error {
	for _, entry := range entries {
		target := filepath.Join(stage, filepath.FromSlash(entry.rel))
		mode := entry.file.Mode().Perm()
		if entry.isDir {
			if mode == 0 {
				mode = 0o755
			}
			if err := os.MkdirAll(target, mode); err != nil {
				return fmt.Errorf("恢复备份：创建临时目录 %s：%w", entry.rel, err)
			}
			result.DirectoryCount++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("恢复备份：创建 %s 的上级目录：%w", entry.rel, err)
		}
		if mode == 0 {
			mode = 0o600
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return fmt.Errorf("恢复备份：创建临时文件 %s：%w", entry.rel, err)
		}
		in, err := entry.file.Open()
		if err != nil {
			_ = out.Close()
			return fmt.Errorf("恢复备份：读取 ZIP 条目 %s：%w", entry.rel, err)
		}
		written, copyErr := io.Copy(out, in)
		inErr := in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return fmt.Errorf("恢复备份：解压 %s（ZIP 可能已损坏）：%w", entry.rel, copyErr)
		}
		if inErr != nil {
			return fmt.Errorf("恢复备份：校验 %s（ZIP 可能已损坏）：%w", entry.rel, inErr)
		}
		if closeErr != nil {
			return fmt.Errorf("恢复备份：写入临时文件 %s：%w", entry.rel, closeErr)
		}
		_ = os.Chtimes(target, entry.file.Modified, entry.file.Modified)
		result.FileCount++
		result.BytesWritten += written
	}
	return nil
}

func preflightRestoreDestination(destination string, entries []restoreEntry, overwrite bool) (bool, error) {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("恢复备份：检查目标目录：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("恢复备份：目标 %s 不是安全的普通目录", destination)
	}
	for _, entry := range entries {
		target := filepath.Join(destination, filepath.FromSlash(entry.rel))
		if err := verifyRestoreParents(destination, filepath.Dir(target)); err != nil {
			return false, err
		}
		existing, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("恢复备份：检查已有路径 %s：%w", entry.rel, err)
		}
		if entry.isDir && existing.IsDir() && existing.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if !overwrite {
			return false, fmt.Errorf("恢复备份：目标已存在 %s；如需替换请启用 Overwrite", entry.rel)
		}
		if entry.isDir != existing.IsDir() {
			return false, fmt.Errorf("恢复备份：不能用%s覆盖已有%s %s", restoreEntryKind(entry.isDir), restoreEntryKind(existing.IsDir()), entry.rel)
		}
	}
	return true, nil
}

func verifyRestoreParents(root, parent string) error {
	rel, err := filepath.Rel(root, parent)
	if err != nil || rel == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("恢复备份：检查目录 %s：%w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("恢复备份：路径 %s 的上级不是安全的普通目录", current)
		}
	}
	return nil
}

func mergeRestoreStage(stage, destination string, entries []restoreEntry, overwrite bool) error {
	directories := make([]restoreEntry, 0)
	files := make([]restoreEntry, 0)
	for _, entry := range entries {
		if entry.isDir {
			directories = append(directories, entry)
		} else {
			files = append(files, entry)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.Count(directories[i].rel, "/") < strings.Count(directories[j].rel, "/")
	})
	for _, entry := range directories {
		target := filepath.Join(destination, filepath.FromSlash(entry.rel))
		info, err := os.Lstat(target)
		if err == nil && !info.IsDir() {
			return fmt.Errorf("恢复备份：目录位置被文件占用 %s", entry.rel)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("恢复备份：检查 %s：%w", entry.rel, err)
		}
		mode := entry.file.Mode().Perm()
		if mode == 0 {
			mode = 0o755
		}
		if err := os.MkdirAll(target, mode); err != nil {
			return fmt.Errorf("恢复备份：创建目录 %s：%w", entry.rel, err)
		}
	}
	for _, entry := range files {
		source := filepath.Join(stage, filepath.FromSlash(entry.rel))
		target := filepath.Join(destination, filepath.FromSlash(entry.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("恢复备份：创建 %s 的上级目录：%w", entry.rel, err)
		}
		if overwrite {
			if err := replaceFileAtomic(source, target); err != nil {
				return fmt.Errorf("恢复备份：原子写入 %s：%w", entry.rel, err)
			}
		} else {
			// Hard-linking refuses to overwrite a file created after preflight.
			if err := os.Link(source, target); err != nil {
				return fmt.Errorf("恢复备份：写入 %s（目标可能已存在）：%w", entry.rel, err)
			}
			if err := os.Remove(source); err != nil {
				return fmt.Errorf("恢复备份：清理临时文件 %s：%w", entry.rel, err)
			}
		}
	}
	// Apply directory metadata after moving children, otherwise directory
	// modification times would immediately change again.
	for i := len(directories) - 1; i >= 0; i-- {
		entry := directories[i]
		target := filepath.Join(destination, filepath.FromSlash(entry.rel))
		mode := entry.file.Mode().Perm()
		if mode != 0 {
			_ = os.Chmod(target, mode)
		}
		_ = os.Chtimes(target, entry.file.Modified, entry.file.Modified)
	}
	return nil
}

func restoreEntryKind(directory bool) string {
	if directory {
		return "目录"
	}
	return "文件"
}

func rejectSymlinkAncestors(filePath string) error {
	current, err := filepath.Abs(filePath)
	if err != nil {
		return err
	}
	for {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("路径包含符号链接 %s", current)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func containedRelativePath(root, candidate string) string {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.Clean(rel)
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
