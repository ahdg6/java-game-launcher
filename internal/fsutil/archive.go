package fsutil

import (
	"errors"
	"path"
	"strings"
)

// SecureArchivePath normalizes an archive entry and rejects paths that could
// escape an extraction root. An empty result denotes an archive root entry.
func SecureArchivePath(name string) (string, error) {
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
