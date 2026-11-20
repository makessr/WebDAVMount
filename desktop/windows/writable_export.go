package desktop

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"webdavmount/internal/providers"

	"golang.org/x/sys/windows"
)

var errWritableExportChanged = errors.New("缓存文件正在使用或已变化。请关闭编辑器后重试导出")

type WritableExportResult struct {
	Path    string `json:"path"`
	Files   int    `json:"files"`
	Skipped int    `json:"skipped"`
}

type WritableCacheItem struct {
	Path   string `json:"path"`
	Reason string `json:"reason,omitempty"`
}

type WritableCachePreview struct {
	Items   []WritableCacheItem `json:"items"`
	Ready   int                 `json:"ready"`
	Skipped int                 `json:"skipped"`
}

func (s *DesktopService) WritableCachePreview() (WritableCachePreview, error) {
	ctx, finish, err := s.beginOperation("writable.cache.preview")
	if err != nil {
		return WritableCachePreview{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.writable.Load()
	if session == nil || session.ID == "" {
		return WritableCachePreview{}, errWritableJournal
	}
	p, err := s.find(session.ID)
	if err != nil || !writableMountProfileSupported(p) || writableFingerprint(p) != session.Fingerprint {
		return WritableCachePreview{}, errWritableJournal
	}
	info, err := os.Stat(session.Cache)
	if err != nil || !info.IsDir() || noReparse(session.Cache) != nil {
		return WritableCachePreview{}, errors.New("原写回缓存暂不可访问")
	}
	alias := filepath.Join(writableProfile(p, session.Epoch).ID, filepath.FromSlash(providers.RemoteRoot(p.Provider, p.URL)))
	plan, err := planWritableExport(ctx, filepath.Join(session.Cache, "vfs", alias), filepath.Join(session.Cache, "vfsMeta", alias))
	if err != nil {
		return WritableCachePreview{}, err
	}
	items := make([]WritableCacheItem, 0, len(plan.entries)+len(plan.skipped))
	items = append(items, plan.skipped...)
	for _, entry := range plan.entries {
		items = append(items, WritableCacheItem{Path: entry.name})
	}
	return WritableCachePreview{Items: items, Ready: len(plan.entries), Skipped: len(plan.skipped)}, nil
}

type writableExportEntry struct {
	name   string
	info   os.FileInfo
	meta   [32]byte
	digest [32]byte
}

type writableExportPlan struct {
	entries       []writableExportEntry
	skipped       []WritableCacheItem
	bytes         int64
	summaryDigest [32]byte
	summarySize   int
}

func (s *DesktopService) ExportWritableCache() (*WritableExportResult, error) {
	return s.exportWritableCache(false)
}

func (s *DesktopService) ArchiveWritableSession() (*WritableExportResult, error) {
	return s.exportWritableCache(true)
}

func (s *DesktopService) exportWritableCache(endSession bool) (*WritableExportResult, error) {
	if s.chooseMountedArchive == nil {
		return nil, errors.New("当前环境无法打开 ZIP 保存窗口")
	}
	ctx, finish, err := s.beginOperation("writable.cache.export")
	if err != nil {
		return nil, err
	}
	defer finish()
	session := s.writable.Load()
	if session == nil || session.ID == "" {
		return nil, errors.New("没有可供导出的可写会话")
	}
	destination, err := s.chooseMountedArchive("WebDAVMount-写回副本-" + time.Now().Format("20060102-150405") + ".zip")
	if err != nil {
		return nil, errors.New("无法打开 ZIP 保存窗口")
	}
	if destination == "" {
		return nil, nil
	}
	if !strings.EqualFold(filepath.Ext(destination), ".zip") {
		return nil, errors.New("请使用 .zip 扩展名")
	}
	folder, err := s.localUserFileFolder(destination)
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	if migrationWithin(session.Cache, destination) {
		return nil, errors.New("请在写回缓存目录以外保存 ZIP")
	}
	if err = mountedSaveTargetAvailable(destination); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(session.ID)
	if err != nil || s.writable.Load() != session || !writableMountProfileSupported(p) || writableFingerprint(p) != session.Fingerprint {
		return nil, errWritableJournal
	}
	if endSession {
		info, err := os.Stat(session.Cache)
		if err != nil || !info.IsDir() || noReparse(session.Cache) != nil {
			return nil, errors.New("原写回缓存暂不可访问，写回会话仍保留")
		}
		if err = checkCollectionBeforeArchive(p, session); err != nil {
			return nil, err
		}
		if err = s.detachWritableForArchive(ctx, p, session); err != nil {
			return nil, err
		}
		if err = checkCollectionBeforeArchive(p, session); err != nil {
			return nil, err
		}
	}
	alias := filepath.Join(writableProfile(p, session.Epoch).ID, filepath.FromSlash(providers.RemoteRoot(p.Provider, p.URL)))
	data := filepath.Join(session.Cache, "vfs", alias)
	meta := filepath.Join(session.Cache, "vfsMeta", alias)
	defer s.setMountedFileSaveProgress(MountedFileSaveProgress{})
	s.setMountedFileSaveProgress(MountedFileSaveProgress{Active: true, Stage: "scanningWriteback"})
	plan, err := planWritableExport(ctx, data, meta)
	if err != nil {
		return nil, err
	}
	if !endSession && len(plan.entries) == 0 && len(plan.skipped) == 0 {
		return nil, errors.New("写回缓存中没有待上传的文件")
	}
	if err = writeWritableExport(ctx, data, meta, &plan, destination, endSession, s.setMountedFileSaveProgress); err != nil {
		return nil, err
	}
	if endSession {
		s.setMountedFileSaveProgress(MountedFileSaveProgress{Active: true, Stage: "verifyingWriteback"})
		if err = s.completeWritableArchive(ctx, p, plan, destination); err != nil {
			return nil, err
		}
	}
	return &WritableExportResult{Path: destination, Files: len(plan.entries), Skipped: len(plan.skipped)}, nil
}

func walkWritableExport(ctx context.Context, root string, visit func(string, string, fs.DirEntry) error) error {
	if err := noReparse(root); err != nil {
		return errWritableJournal
	}
	items := 0
	names := make(map[string]bool)
	return filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return errors.New("操作已取消")
		}
		if err != nil {
			if current == root && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return errWritableExportChanged
		}
		if current == root {
			if !entry.IsDir() {
				return errWritableJournal
			}
			return nil
		}
		items++
		if items > mountedArchiveEntryLimit {
			return errors.New("写回缓存超过导出上限（10,000 项）")
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return errWritableJournal
		}
		name := filepath.ToSlash(relative)
		if !validSearchPath(name) || !safeWindowsFileParts(name) || strings.Count(name, "/") > mountedArchiveDepthLimit {
			return errors.New("写回缓存包含无法安全解压的路径")
		}
		key := strings.ToUpper(name)
		if names[key] {
			return errors.New("写回缓存中有仅大小写不同的名称，无法生成安全的 ZIP")
		}
		names[key] = true
		if noReparse(current) != nil {
			return errors.New("写回缓存路径包含链接或已变化")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errWritableJournal
		}
		return visit(current, name, entry)
	})
}

func planWritableExport(ctx context.Context, data, meta string) (writableExportPlan, error) {
	plan := writableExportPlan{}
	indexed := make(map[string]bool)
	skip := func(name, reason string) {
		plan.skipped = append(plan.skipped, WritableCacheItem{Path: name, Reason: reason})
	}
	err := walkWritableExport(ctx, meta, func(current, name string, _ fs.DirEntry) error {
		key := strings.ToUpper(name)
		indexed[key] = true
		info, tag, err := readWritableCacheMetadata(current)
		if err != nil {
			skip(name, "索引无法读取")
			return nil
		}
		if !*info.Dirty {
			return nil
		}
		dataPath := filepath.Join(data, filepath.FromSlash(name))
		if !info.complete() || noReparse(dataPath) != nil {
			skip(name, "缓存内容不完整或不可访问")
			return nil
		}
		stat, err := os.Stat(dataPath)
		if err != nil || !stat.Mode().IsRegular() || stat.Size() != *info.Size {
			skip(name, "缓存文件缺失或大小与索引不一致")
			return nil
		}
		if *info.Size > math.MaxInt64-plan.bytes {
			return errors.New("待导出的文件总大小超出范围")
		}
		plan.entries = append(plan.entries, writableExportEntry{name: name, info: stat, meta: tag})
		plan.bytes += *info.Size
		return nil
	})
	if err != nil {
		return plan, err
	}
	err = walkWritableExport(ctx, data, func(_, name string, _ fs.DirEntry) error {
		if !indexed[strings.ToUpper(name)] {
			skip(name, "缺少索引，无法确认内容完整")
		}
		return nil
	})
	return plan, err
}

func writeWritableExport(ctx context.Context, data, meta string, plan *writableExportPlan, destination string, endSession bool, update func(MountedFileSaveProgress)) error {
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".wdm-save-*.tmp")
	if err != nil {
		return errors.New("无法创建临时 ZIP，请检查目标目录权限")
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	writer := zip.NewWriter(tmp)
	buffer := make([]byte, 256<<10)
	progress := MountedFileSaveProgress{Active: true, Stage: "archiving", Count: len(plan.entries), Total: plan.bytes}
	update(progress)
	for i := range plan.entries {
		entry := &plan.entries[i]
		if ctx.Err() != nil {
			return errors.New("操作已取消")
		}
		header, err := zip.FileInfoHeader(entry.info)
		if err != nil {
			return errors.New("无法生成 ZIP 文件项")
		}
		header.Name = "文件/" + entry.name
		header.Method = zip.Store
		target, err := writer.CreateHeader(header)
		if err != nil {
			return errors.New("无法写入 ZIP 文件，请检查磁盘空间")
		}
		progress.Index++
		progress.Name = path.Base(entry.name)
		update(progress)
		base := progress.Bytes
		var contents io.Writer = target
		digest := sha256.New()
		if endSession {
			contents = io.MultiWriter(target, digest)
		}
		if err := copyWritableExportEntry(ctx, data, meta, *entry, contents, buffer, func(count int64) {
			progress.Bytes = base + count
			update(progress)
		}); err != nil {
			return err
		}
		if endSession {
			copy(entry.digest[:], digest.Sum(nil))
		}
	}
	note, err := writer.Create("恢复说明.txt")
	if err != nil {
		return errors.New("无法写入恢复说明")
	}
	usage := "写回队列和原缓存由客户端继续保留。"
	if endSession {
		usage = "客户端确认结束会话后，这批写回停止重试。原缓存文件仍留在当前缓存位置。"
	}
	skippedText := make([]string, 0, len(plan.skipped))
	for _, item := range plan.skipped {
		skippedText = append(skippedText, fmt.Sprintf("%q  %s", item.Path, item.Reason))
	}
	summary := fmt.Sprintf("WebDAVMount 写回副本\r\n\r\n已导出 %d 个文件，跳过 %d 项。\r\n文件位于「文件」目录。%s\r\n\r\n%s\r\n",
		len(plan.entries), len(plan.skipped), usage, strings.Join(skippedText, "\r\n"))
	plan.summaryDigest = sha256.Sum256([]byte(summary))
	plan.summarySize = len(summary)
	if _, err = io.WriteString(note, summary); err != nil {
		return errors.New("无法写入恢复说明")
	}
	if err = writer.Close(); err != nil {
		return errors.New("无法完成 ZIP 文件，请检查磁盘空间")
	}
	return publishMountedSave(ctx, tmp, destination)
}

func copyWritableExportEntry(ctx context.Context, data, meta string, entry writableExportEntry, target io.Writer, buffer []byte, update func(int64)) error {
	local := filepath.FromSlash(entry.name)
	source, _, err := migrationOpen(filepath.Join(data, local), windows.GENERIC_READ, windows.OPEN_EXISTING, false)
	if err != nil {
		return errWritableExportChanged
	}
	defer source.Close()
	current, err := source.Stat()
	if err != nil || !os.SameFile(current, entry.info) || current.Size() != entry.info.Size() || !current.ModTime().Equal(entry.info.ModTime()) {
		return errWritableExportChanged
	}
	index := filepath.Join(meta, local)
	_, hash, err := readWritableCacheMetadata(index)
	if err != nil || hash != entry.meta {
		return errWritableExportChanged
	}
	if err = copyMountedFileContents(ctx, source, current, target, buffer, update); err != nil {
		return err
	}
	_, hash, err = readWritableCacheMetadata(index)
	if err != nil || hash != entry.meta {
		return errWritableExportChanged
	}
	return nil
}
