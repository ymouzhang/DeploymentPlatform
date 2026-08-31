package remote

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"strings"
	"time"

	"DP/internal/domain"

	"github.com/pkg/sftp"
)

const modelMarker = ".dp-model.json"

type ModelArchiveInspection struct {
	SHA256          string
	ExpandedSize    int64
	FileCount       int64
	StripCommonRoot bool
}

func ModelUploadRemotePath(targetDir, uploadID string) string {
	return path.Join(path.Dir(targetDir), ".dp-model-upload-"+uploadID+".tar.gz")
}

func (e *Executor) PrepareModelUpload(ctx context.Context, env domain.Environment, password []byte, targetDir, remotePath string, totalBytes int64) error {
	client, _, err := e.connect(ctx, env, password)
	if err != nil {
		return err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("start SFTP: %w", err)
	}
	defer sftpClient.Close()
	parent := path.Dir(targetDir)
	if err := sftpClient.MkdirAll(parent); err != nil {
		return fmt.Errorf("创建模型父目录失败: %w", err)
	}
	if _, err := sftpClient.Stat(targetDir); err == nil {
		return &domain.AppError{Code: "MODEL_TARGET_EXISTS", Message: "目标目录已经存在，已拒绝覆盖"}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查模型目标目录失败: %w", err)
	}
	if _, err := sftpClient.Stat(remotePath); err == nil {
		return &domain.AppError{Code: "MODEL_UPLOAD_EXISTS", Message: "远端上传暂存文件已经存在"}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查远端暂存文件失败: %w", err)
	}
	if err := requireRemoteSpace(sftpClient, parent, totalBytes); err != nil {
		return err
	}
	file, err := sftpClient.OpenFile(remotePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return fmt.Errorf("创建远端模型暂存文件失败: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		_ = sftpClient.Remove(remotePath)
		return fmt.Errorf("同步远端模型暂存文件失败: %w", syncErr)
	}
	if closeErr == nil {
		closeErr = sftpClient.Chmod(remotePath, 0o600)
	}
	if closeErr != nil {
		_ = sftpClient.Remove(remotePath)
	}
	return closeErr
}

func (e *Executor) ModelUploadOffset(ctx context.Context, env domain.Environment, password []byte, remotePath string) (int64, error) {
	client, _, err := e.connect(ctx, env, password)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return 0, fmt.Errorf("start SFTP: %w", err)
	}
	defer sftpClient.Close()
	info, err := sftpClient.Stat(remotePath)
	if os.IsNotExist(err) {
		return 0, &domain.AppError{Code: "MODEL_UPLOAD_REMOTE_MISSING", Message: "远端上传暂存文件不存在"}
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (e *Executor) AppendModelChunk(ctx context.Context, env domain.Environment, password []byte, remotePath string, expectedOffset, size int64, reader io.Reader) (int64, error) {
	client, _, err := e.connect(ctx, env, password)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return 0, fmt.Errorf("start SFTP: %w", err)
	}
	defer sftpClient.Close()
	info, err := sftpClient.Stat(remotePath)
	if err != nil {
		return 0, fmt.Errorf("读取远端上传进度失败: %w", err)
	}
	if info.Size() != expectedOffset {
		return info.Size(), &domain.AppError{Code: "UPLOAD_OFFSET_MISMATCH", Message: "上传偏移与远端文件不一致"}
	}
	file, err := sftpClient.OpenFile(remotePath, os.O_WRONLY)
	if err != nil {
		return expectedOffset, fmt.Errorf("打开远端暂存文件失败: %w", err)
	}
	defer file.Close()
	if _, err := file.Seek(expectedOffset, io.SeekStart); err != nil {
		return expectedOffset, err
	}
	written, copyErr := io.Copy(file, io.LimitReader(&contextReader{ctx: ctx, r: reader}, size+1))
	if written != size && copyErr == nil {
		copyErr = io.ErrUnexpectedEOF
	}
	if written > size && copyErr == nil {
		copyErr = errors.New("分片内容超过声明大小")
	}
	if copyErr != nil {
		_ = file.Truncate(expectedOffset)
		_ = file.Sync()
		written = 0
	} else {
		if syncErr := file.Sync(); syncErr != nil {
			copyErr = syncErr
			_ = file.Truncate(expectedOffset)
			_ = file.Sync()
			written = 0
		}
	}
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return expectedOffset, errors.Join(copyErr, closeErr)
	}
	return expectedOffset + written, nil
}

func (e *Executor) RemoveModelUpload(ctx context.Context, env domain.Environment, password []byte, remotePath string) error {
	client, _, err := e.connect(ctx, env, password)
	if err != nil {
		return err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sftpClient.Close()
	err = sftpClient.Remove(remotePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (e *Executor) InspectModelArchive(ctx context.Context, env domain.Environment, password []byte, remotePath string, maxExpanded int64) (ModelArchiveInspection, error) {
	client, _, err := e.connect(ctx, env, password)
	if err != nil {
		return ModelArchiveInspection{}, err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return ModelArchiveInspection{}, err
	}
	defer sftpClient.Close()
	file, err := sftpClient.Open(remotePath)
	if err != nil {
		return ModelArchiveInspection{}, fmt.Errorf("打开远端模型包失败: %w", err)
	}
	defer file.Close()
	return inspectModelArchive(&contextReader{ctx: ctx, r: file}, maxExpanded)
}

func inspectModelArchive(reader io.Reader, maxExpanded int64) (ModelArchiveInspection, error) {
	hash := sha256.New()
	gz, err := gzip.NewReader(io.TeeReader(reader, hash))
	if err != nil {
		return ModelArchiveInspection{}, &domain.AppError{Code: "MODEL_ARCHIVE_INVALID", Message: "模型包不是有效的 gzip 文件", Err: err}
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	result := ModelArchiveInspection{StripCommonRoot: true}
	var commonRoot string
	seenPaths := make(map[string]struct{})
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, &domain.AppError{Code: "MODEL_ARCHIVE_INVALID", Message: "模型 tar 结构损坏", Err: err}
		}
		name := header.Name
		if name == "" || len(name) > 4096 || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || hasParentSegment(name) {
			return result, unsafeArchiveError()
		}
		cleaned := path.Clean(name)
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return result, unsafeArchiveError()
		}
		if _, exists := seenPaths[cleaned]; exists {
			return result, &domain.AppError{Code: "MODEL_ARCHIVE_UNSAFE", Message: "模型包包含重复路径"}
		}
		seenPaths[cleaned] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || result.ExpandedSize > math.MaxInt64-header.Size {
				return result, &domain.AppError{Code: "MODEL_ARCHIVE_TOO_LARGE", Message: "模型展开大小超出限制"}
			}
			result.FileCount++
			result.ExpandedSize += header.Size
			if result.ExpandedSize > maxExpanded {
				return result, &domain.AppError{Code: "MODEL_ARCHIVE_TOO_LARGE", Message: "模型展开大小超出限制"}
			}
		default:
			return result, &domain.AppError{Code: "MODEL_ARCHIVE_UNSAFE", Message: "模型包不能包含符号链接、硬链接或设备文件"}
		}
		parts := strings.SplitN(cleaned, "/", 2)
		if len(parts) < 2 && header.Typeflag != tar.TypeDir {
			result.StripCommonRoot = false
		}
		if commonRoot == "" {
			commonRoot = parts[0]
		} else if commonRoot != parts[0] {
			result.StripCommonRoot = false
		}
		if result.FileCount > 5_000_000 {
			return result, &domain.AppError{Code: "MODEL_ARCHIVE_TOO_MANY_FILES", Message: "模型包文件数量超出限制"}
		}
	}
	if result.FileCount == 0 {
		return result, &domain.AppError{Code: "MODEL_ARCHIVE_EMPTY", Message: "模型包中没有普通文件"}
	}
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return result, err
	}
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func (e *Executor) DeployModelArchive(ctx context.Context, env domain.Environment, password []byte, model domain.Model, uploadID string, inspection ModelArchiveInspection, emit EmitFunc) error {
	client, _, err := e.connect(ctx, env, password)
	if err != nil {
		return err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sftpClient.Close()
	parent := path.Dir(model.TargetDir)
	if _, err := sftpClient.Stat(model.TargetDir); err == nil {
		matched, markerErr := modelMarkerMatches(sftpClient, model.TargetDir, model.ID, model.MarkerOwnerID, inspection.SHA256)
		if markerErr == nil && matched {
			emit("system", "检测到已提交的模型目录，已根据远端标记恢复本地状态")
			_ = sftpClient.Remove(ModelUploadRemotePath(model.TargetDir, uploadID))
			return nil
		}
		return &domain.AppError{Code: "MODEL_TARGET_EXISTS", Message: "目标目录已经存在，已拒绝覆盖", Err: markerErr}
	} else if !os.IsNotExist(err) {
		return err
	}
	reserve := inspection.ExpandedSize / 20
	if reserve < 1<<30 {
		reserve = 1 << 30
	}
	if inspection.ExpandedSize > math.MaxInt64-reserve {
		return &domain.AppError{Code: "MODEL_DISK_SPACE_INSUFFICIENT", Message: "模型展开大小超出磁盘容量计算范围"}
	}
	if err := requireRemoteSpace(sftpClient, parent, inspection.ExpandedSize+reserve); err != nil {
		return err
	}
	tempDir := path.Join(parent, ".dp-model-extract-"+uploadID)
	if _, statErr := sftpClient.Stat(tempDir); statErr == nil {
		emit("system", "正在清理上次未完成任务的专属解压目录")
		cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, err = runCommand(cleanupCtx, client, "rm -rf -- "+shellQuote(tempDir), password, emit)
		cancel()
		if err != nil {
			return fmt.Errorf("清理上次解压目录失败: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := sftpClient.Mkdir(tempDir); err != nil {
		return fmt.Errorf("创建远端解压目录失败: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_, _ = runCommand(cleanupCtx, client, "rm -rf -- "+shellQuote(tempDir), password, func(string, string) {})
		}
	}()
	remotePath := ModelUploadRemotePath(model.TargetDir, uploadID)
	command := "tar -xzf " + shellQuote(remotePath) + " -C " + shellQuote(tempDir)
	if inspection.StripCommonRoot {
		command += " --strip-components=1"
	}
	emit("system", "开始在目标机解压模型")
	if _, err := runCommand(ctx, client, command, password, emit); err != nil {
		return fmt.Errorf("远端模型解压失败: %w", err)
	}
	marker := map[string]any{"schema_version": 1, "model_id": model.ID, "owner_id": model.MarkerOwnerID,
		"name": model.Name, "source": "upload", "archive_sha256": inspection.SHA256, "created_at": time.Now().UTC()}
	if err := writeJSONFile(sftpClient, path.Join(tempDir, modelMarker), marker); err != nil {
		return fmt.Errorf("写入模型标记失败: %w", err)
	}
	if err := sftpClient.PosixRename(tempDir, model.TargetDir); err != nil {
		return fmt.Errorf("提交模型目录失败: %w", err)
	}
	cleanup = false
	if err := sftpClient.Remove(remotePath); err != nil && !os.IsNotExist(err) {
		emit("system", "模型已就绪，但清理压缩包失败: "+err.Error())
	}
	return nil
}

func (e *Executor) DeleteModel(ctx context.Context, env domain.Environment, password []byte, model domain.Model, taskID string, emit EmitFunc) error {
	client, _, err := e.connect(ctx, env, password)
	if err != nil {
		return err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sftpClient.Close()
	markerFile, err := sftpClient.Open(path.Join(model.TargetDir, modelMarker))
	if os.IsNotExist(err) {
		return &domain.AppError{Code: "MODEL_MARKER_MISSING", Message: "目标目录缺少 DP 模型标记，已拒绝删除"}
	}
	if err != nil {
		return err
	}
	var marker struct {
		ModelID string `json:"model_id"`
		OwnerID string `json:"owner_id"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(markerFile, 1<<20)).Decode(&marker)
	_ = markerFile.Close()
	if decodeErr != nil || marker.ModelID != model.ID || marker.OwnerID != model.MarkerOwnerID {
		return &domain.AppError{Code: "MODEL_MARKER_MISMATCH", Message: "目标目录模型标记与当前记录不匹配，已拒绝删除"}
	}
	trash := path.Join(path.Dir(model.TargetDir), ".dp-model-trash-"+taskID)
	if err := sftpClient.PosixRename(model.TargetDir, trash); err != nil {
		return fmt.Errorf("隔离待删除模型目录失败: %w", err)
	}
	emit("system", "模型目录已从服务路径隔离，正在释放磁盘空间")
	if _, err := runCommand(ctx, client, "rm -rf -- "+shellQuote(trash), password, emit); err != nil {
		return fmt.Errorf("清理模型目录失败: %w", err)
	}
	return nil
}

func (e *Executor) ModelTargetOwned(ctx context.Context, env domain.Environment, password []byte, model domain.Model) (bool, error) {
	client, _, err := e.connect(ctx, env, password)
	if err != nil {
		return false, err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return false, err
	}
	defer sftpClient.Close()
	if _, err := sftpClient.Stat(model.TargetDir); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	matched, err := modelMarkerMatches(sftpClient, model.TargetDir, model.ID, model.MarkerOwnerID, "")
	if err != nil || !matched {
		return false, &domain.AppError{Code: "MODEL_MARKER_MISMATCH", Message: "目标目录存在但模型归属标记不匹配", Err: err}
	}
	return true, nil
}

func requireRemoteSpace(client *sftp.Client, directory string, required int64) error {
	stats, err := client.StatVFS(directory)
	if err != nil {
		return fmt.Errorf("读取目标磁盘空间失败: %w", err)
	}
	available := stats.FreeSpace()
	if required < 0 || uint64(required) > available {
		return &domain.AppError{Code: "MODEL_DISK_SPACE_INSUFFICIENT", Message: fmt.Sprintf("目标磁盘空间不足，需要至少 %d 字节，可用 %d 字节", required, available)}
	}
	return nil
}

func unsafeArchiveError() error {
	return &domain.AppError{Code: "MODEL_ARCHIVE_UNSAFE", Message: "模型包包含不安全路径"}
}

func hasParentSegment(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func modelMarkerMatches(client *sftp.Client, targetDir, modelID, ownerID, sha string) (bool, error) {
	file, err := client.Open(path.Join(targetDir, modelMarker))
	if err != nil {
		return false, err
	}
	defer file.Close()
	var marker struct {
		ModelID       string `json:"model_id"`
		OwnerID       string `json:"owner_id"`
		ArchiveSHA256 string `json:"archive_sha256"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&marker); err != nil {
		return false, err
	}
	return marker.ModelID == modelID && marker.OwnerID == ownerID && (sha == "" || marker.ArchiveSHA256 == sha), nil
}
