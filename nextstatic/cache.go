package nextstatic

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// defaultHTMLCacheMaxBytes HTML 内存缓存的默认总量上限
const defaultHTMLCacheMaxBytes = 64 << 20

// snapshot 导出目录在某一时刻的快照: 文件索引 + 按需填充的 HTML 缓存。
// 路径解析的存在性判断全部走索引, 未命中请求 (扫描器打随机路径) 不再产生
// 一串 stat 系统调用; 快照整体替换 (Reload), 自身的 files 构建后只读
type snapshot struct {
	// files 普通文件集合, key 为相对导出根、以 "/" 开头的斜杠路径
	files map[string]struct{}

	htmlMax   int64
	mu        sync.RWMutex
	html      map[string][]byte
	htmlBytes int64
}

// buildSnapshot 遍历导出目录建立文件索引。
// 指向普通文件的符号链接会被收录; 符号链接目录不展开
func buildSnapshot(root string, htmlMax int64) (*snapshot, error) {
	// WalkDir 不跟随符号链接, Root 本身是链接时需先解析
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("nextstatic: resolve root %q: %w", root, err)
	}

	files := make(map[string]struct{})
	err = filepath.WalkDir(resolved, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			info, statErr := os.Stat(p)
			if statErr != nil || info.IsDir() {
				return nil
			}
		}
		rel, relErr := filepath.Rel(resolved, p)
		if relErr != nil {
			return relErr
		}
		files["/"+filepath.ToSlash(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("nextstatic: index root %q: %w", root, err)
	}

	if htmlMax == 0 {
		htmlMax = defaultHTMLCacheMaxBytes
	}
	return &snapshot{files: files, htmlMax: htmlMax, html: make(map[string][]byte)}, nil
}

// readHTML 返回 HTML 文件内容, 首次读盘后留在内存; 缓存总量达到上限后
// 新文件不再入缓存 (退化为每次读盘), 不做淘汰。
// 返回的切片被所有请求共享, 容量已收紧到长度, 调用方 append 必然重新分配
func (s *snapshot) readHTML(key, fullPath string) ([]byte, error) {
	s.mu.RLock()
	body, ok := s.html[key]
	s.mu.RUnlock()
	if ok {
		return body, nil
	}

	body, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, err
	}
	body = body[:len(body):len(body)]

	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.html[key]; ok {
		return cached, nil // 并发首读, 以先入缓存者为准
	}
	if s.htmlBytes+int64(len(body)) <= s.htmlMax {
		s.html[key] = body
		s.htmlBytes += int64(len(body))
	}
	return body, nil
}

// fileExists 判断路径存在且不是目录 (DisableCache 模式的读盘判断)
func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
