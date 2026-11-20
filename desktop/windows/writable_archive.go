package desktop

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const collectionJournalDirectory = "webdavmount-collection-journal"

func checkCollectionBeforeArchive(p Profile, session *writableSession) error {
	if err := checkS3DeleteBeforeEnding(p, session); err != nil {
		return err
	}
	pending, err := hasCollectionReview(p, session.Cache, session.Epoch)
	if err != nil {
		return err
	}
	if pending {
		return errors.New("有目录操作待确认，请先核对结果，再结束写回")
	}
	return nil
}

func checkS3DeleteBeforeEnding(p Profile, session *writableSession) error {
	if !s3CandidateProfile(p) {
		return nil
	}
	record := s3DeleteJournalFile(p, session)
	if err := noReparse(record); err != nil {
		return errWritableJournal
	}
	if _, err := os.Lstat(record); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return errWritableJournal
	}
	return errors.New("有 S3 删除结果待确认，请先核对")
}

func s3DeleteJournalFile(p Profile, session *writableSession) string {
	digest := sha256.Sum256([]byte(writableProfile(p, session.Epoch).ID))
	return filepath.Join(session.Cache, "vfsMeta", ".wdm-s3-writeback", "delete-"+hex.EncodeToString(digest[:])+".json")
}

func hasCollectionReview(p Profile, cacheRoot, epoch string) (bool, error) {
	if !supportsWritable(p) {
		return false, nil
	}
	// The directory-operation candidate hashes rclone's absolute Win32
	// extended VFS data path, including the current cache generation.
	root := filepath.Join(cacheRoot, "vfs", writableProfile(p, epoch).ID)
	if !strings.HasPrefix(root, `\\?\`) {
		root = `\\?\` + root
	}
	key := sha256.Sum256([]byte(root))
	record := filepath.Join(cacheRoot, collectionJournalDirectory, hex.EncodeToString(key[:])+".json")
	if err := noReparse(record); err != nil {
		return false, errWritableJournal
	}
	if _, err := os.Lstat(record); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, errWritableJournal
	}
	return true, nil
}

func (s *DesktopService) connectionDirectoryReview(p Profile) (bool, error) {
	if !supportsWritable(p) {
		return false, nil
	}
	cacheRoot := s.cacheRoot
	var epoch string
	if session := s.writable.Load(); session != nil && session.ID == p.ID {
		cacheRoot, epoch = session.Cache, session.Epoch
	} else {
		var err error
		epoch, err = s.readWritableEpoch(p)
		if err != nil {
			return false, err
		}
	}
	return hasCollectionReview(p, cacheRoot, epoch)
}

func (s *DesktopService) checkConnectionDirectoryReview(p Profile) error {
	pending, err := s.connectionDirectoryReview(p)
	if err != nil {
		return err
	}
	if pending {
		return errors.New("有目录操作待确认，请重新挂载对应网盘并核对结果，再编辑或删除连接")
	}
	return nil
}

func (s *DesktopService) pendingDirectoryReviewIDs() []string {
	if checkCacheLocationReviews(s.cacheRoot) == nil {
		return nil
	}
	var ids []string
	for _, p := range s.profiles {
		if pending, err := s.connectionDirectoryReview(p); err == nil && pending {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// The caller holds the desktop mutex. Unmount only the VFS in this session;
// a different profile may be using its former drive letter.
func (s *DesktopService) detachWritableForArchive(ctx context.Context, p Profile, session *writableSession) error {
	if !s.engineOnline() {
		return nil
	}
	state, err := s.callContext(ctx, "mount/listmounts", nil)
	if err != nil {
		return errors.New("无法确认盘符挂载状态，写回会话仍保留")
	}
	if _, err = identifiedMounts(state, []Profile{p}); err != nil {
		return err
	}
	expected := writableProfile(p, session.Epoch).remoteFS()
	exists := false
	for _, raw := range state["mountPoints"].([]any) {
		entry := raw.(map[string]any)
		point, remote := mountIdentity(entry)
		if strings.EqualFold(point, p.Drive) && writableRemoteMatches(p, remote) {
			if remote != expected {
				return errWritableJournal
			}
			exists = true
		}
	}
	if exists {
		status := s.writableStats(ctx, p)
		if !status.Known || status.Uploading != 0 {
			return errors.New("请关闭编辑器并等待本次上传结束，再保存副本并结束写回")
		}
		if _, err = s.callContext(ctx, "mount/unmount", map[string]any{"mountPoint": p.Drive}); err != nil {
			return errors.New("无法卸载可写盘符，写回会话仍保留")
		}
		delete(s.desired, p.ID)
		s.sessionRecovery.update(p, false)
	}
	vfs, err := s.callContext(ctx, "vfs/list", nil)
	if err != nil {
		return errors.New("无法确认写回停止，写回会话仍保留")
	}
	names, ok := vfs["vfses"].([]any)
	if !ok {
		return errWritableJournal
	}
	for _, item := range names {
		name, ok := item.(string)
		if !ok || name == expected || strings.HasPrefix(name, expected+"[") {
			return errors.New("写回还在停止中，请稍后重试")
		}
	}
	return nil
}

func (s *DesktopService) completeWritableArchive(ctx context.Context, p Profile, plan writableExportPlan, destination string) error {
	// Hold the ZIP open without write/delete sharing until retirement has
	// committed, so an editor or file replacement cannot invalidate the copy.
	file, _, err := migrationOpen(destination, windows.GENERIC_READ, windows.OPEN_EXISTING, false)
	if err != nil {
		return errors.New("无法复核已保存的 ZIP，写回会话仍保留")
	}
	defer file.Close()
	if err = verifyWritableArchive(ctx, file, plan); err != nil {
		return err
	}
	epoch := randomID()
	if err = ctx.Err(); err != nil {
		return errors.New("操作已取消，写回会话仍保留")
	}
	if err = atomicWriteSettings(s.writableEpochPath(p), []byte(epoch)); err != nil {
		return errors.New("ZIP 已保存，无法更新写回缓存位置，会话仍保留")
	}
	// On interruption before the rename the active journal still identifies
	// the old cache, independent of the already published next epoch.
	active := filepath.Join(s.dir, writableJournal)
	retired := filepath.Join(s.dir, "writable-retired-"+epoch+".enc")
	from, fromErr := windows.UTF16PtrFromString(active)
	to, toErr := windows.UTF16PtrFromString(retired)
	if fromErr != nil || toErr != nil || noReparse(active) != nil || noReparse(retired) != nil ||
		windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH) != nil {
		return errors.New("ZIP 已保存，无法归档写回会话。可另存新的副本并重试")
	}
	s.writable.Store(nil)
	return nil
}

func verifyWritableArchive(ctx context.Context, file *os.File, plan writableExportPlan) error {
	failure := errors.New("ZIP 完整性复核失败，写回会话仍保留")
	info, err := file.Stat()
	if err != nil {
		return failure
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil || len(reader.File) != len(plan.entries)+1 {
		return failure
	}
	type archiveProof struct {
		size   uint64
		digest [32]byte
	}
	expected := map[string]archiveProof{"恢复说明.txt": {size: uint64(plan.summarySize), digest: plan.summaryDigest}}
	for _, entry := range plan.entries {
		expected["文件/"+entry.name] = archiveProof{size: uint64(entry.info.Size()), digest: entry.digest}
	}
	buffer := make([]byte, 256<<10)
	for _, entry := range reader.File {
		proof, exists := expected[entry.Name]
		if !exists || entry.UncompressedSize64 != proof.size {
			return failure
		}
		delete(expected, entry.Name)
		stream, err := entry.Open()
		if err != nil {
			return failure
		}
		hash := sha256.New()
		err = readWritableArchiveEntry(ctx, stream, hash, buffer)
		stream.Close()
		if errors.Is(err, context.Canceled) {
			return errors.New("操作已取消，写回会话仍保留")
		}
		if err != nil || !bytes.Equal(hash.Sum(nil), proof.digest[:]) {
			return failure
		}
	}
	return nil
}

func readWritableArchiveEntry(ctx context.Context, stream io.Reader, digest io.Writer, buffer []byte) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := stream.Read(buffer)
		if n > 0 {
			_, _ = digest.Write(buffer[:n])
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
