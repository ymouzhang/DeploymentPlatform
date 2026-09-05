package remote

import (
	"archive/tar"
	"bufio"
	"bytes"
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
	"runtime"
	"strings"
	"sync"
	"time"

	"DP/internal/domain"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const modelMarker = ".dp-model.json"

type ModelValidationProgress func(bytesRead, totalBytes int64)

type ModelArchiveInspection struct {
	SHA256          string `json:"sha256"`
	ExpandedSize    int64  `json:"expanded_size"`
	FileCount       int64  `json:"file_count"`
	StripCommonRoot bool   `json:"strip_common_root"`
}

func ModelUploadRemotePath(targetDir, uploadID string) string {
	return path.Join(path.Dir(targetDir), ".dp-model-upload-"+uploadID+".tar.gz")
}

func (e *Executor) PrepareModelUpload(ctx context.Context, host domain.Host, password []byte, targetDir, remotePath string, totalBytes int64) error {
	client, _, err := e.connectHost(ctx, host, password)
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

func (e *Executor) ModelUploadOffset(ctx context.Context, host domain.Host, password []byte, remotePath string) (int64, error) {
	client, _, err := e.connectHost(ctx, host, password)
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

func (e *Executor) AppendModelChunk(ctx context.Context, host domain.Host, password []byte, remotePath string, expectedOffset, size int64, reader io.Reader) (int64, error) {
	client, _, err := e.connectHost(ctx, host, password)
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

func (e *Executor) RemoveModelUpload(ctx context.Context, host domain.Host, password []byte, remotePath string) error {
	client, _, err := e.connectHost(ctx, host, password)
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

func (e *Executor) InspectModelArchive(ctx context.Context, host domain.Host, password []byte, remotePath string, maxExpanded int64, progress ModelValidationProgress, emit EmitFunc) (ModelArchiveInspection, error) {
	client, _, err := e.connectHost(ctx, host, password)
	if err != nil {
		return ModelArchiveInspection{}, err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client, sftp.UseConcurrentReads(true), sftp.MaxConcurrentRequestsPerFile(256))
	if err != nil {
		return ModelArchiveInspection{}, err
	}
	defer sftpClient.Close()

	validatorPath, toolErr := ensureRemoteModelValidator(ctx, client, sftpClient, path.Dir(remotePath), emit)
	if toolErr == nil {
		inspection, validationErr, structured := runRemoteModelValidator(ctx, client, validatorPath, remotePath, maxExpanded, progress)
		if validationErr == nil || structured {
			return inspection, validationErr
		}
		toolErr = validationErr
	}
	if emit != nil {
		emit("system", "目标机本地校验器不可用，回退到 SFTP 流式校验："+toolErr.Error())
	}
	return inspectModelArchiveViaSFTP(ctx, sftpClient, remotePath, maxExpanded, progress)
}

func inspectModelArchiveViaSFTP(ctx context.Context, sftpClient *sftp.Client, remotePath string, maxExpanded int64, progress ModelValidationProgress) (ModelArchiveInspection, error) {
	file, err := sftpClient.Open(remotePath)
	if err != nil {
		return ModelArchiveInspection{}, fmt.Errorf("打开远端模型包失败: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ModelArchiveInspection{}, err
	}
	pipeReader, pipeWriter := io.Pipe()
	transferDone := make(chan error, 1)
	go func() {
		_, copyErr := file.WriteTo(pipeWriter)
		_ = pipeWriter.CloseWithError(copyErr)
		transferDone <- copyErr
	}()
	reader := &validatorProgressReader{r: &contextReader{ctx: ctx, r: pipeReader}, total: info.Size(), interval: time.Second, report: progress}
	inspection, inspectErr := inspectModelArchive(reader, maxExpanded)
	_ = pipeReader.CloseWithError(inspectErr)
	transferErr := <-transferDone
	if inspectErr != nil {
		return inspection, inspectErr
	}
	if transferErr != nil {
		return inspection, transferErr
	}
	reader.finish()
	return inspection, nil
}

var localModelValidator struct {
	sync.Once
	path string
	sha  string
	size int64
	err  error
}

func localModelValidatorInfo() (string, string, int64, error) {
	localModelValidator.Do(func() {
		localModelValidator.path, localModelValidator.err = os.Executable()
		if localModelValidator.err != nil {
			return
		}
		file, err := os.Open(localModelValidator.path)
		if err != nil {
			localModelValidator.err = err
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			localModelValidator.err = err
			return
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			localModelValidator.err = err
			return
		}
		localModelValidator.sha = hex.EncodeToString(hash.Sum(nil))
		localModelValidator.size = info.Size()
	})
	return localModelValidator.path, localModelValidator.sha, localModelValidator.size, localModelValidator.err
}

func ensureRemoteModelValidator(ctx context.Context, client *ssh.Client, sftpClient *sftp.Client, baseDir string, emit EmitFunc) (string, error) {
	targetArch, err := detectArch(client)
	if err != nil {
		return "", err
	}
	targetArch = normalizeLinuxArch(targetArch)
	if targetArch != runtime.GOARCH {
		return "", fmt.Errorf("目标机架构 %s 与 DP 架构 %s 不一致", targetArch, runtime.GOARCH)
	}
	localPath, localSHA, localSize, err := localModelValidatorInfo()
	if err != nil {
		return "", err
	}
	toolDir := path.Join(baseDir, ".dp-tools")
	if err := sftpClient.MkdirAll(toolDir); err != nil {
		return "", fmt.Errorf("创建远端工具目录失败: %w", err)
	}
	_ = sftpClient.Chmod(toolDir, 0o700)
	remotePath := path.Join(toolDir, "model-validator-"+localSHA[:16])
	if info, statErr := sftpClient.Stat(remotePath); statErr == nil && info.Mode().IsRegular() && info.Size() == localSize {
		remoteSHA, hashErr := hashRemoteFile(sftpClient, remotePath)
		if hashErr == nil && remoteSHA == localSHA {
			return remotePath, nil
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	if emit != nil {
		emit("system", fmt.Sprintf("首次使用，正在上传目标机本地模型校验器（%d 字节）", localSize))
	}
	tempPath := remotePath + ".tmp-" + randomSuffix()
	local, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer local.Close()
	remoteFile, err := sftpClient.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(remoteFile, &contextReader{ctx: ctx, r: local})
	syncErr := remoteFile.Sync()
	closeErr := remoteFile.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = sftpClient.Remove(tempPath)
		return "", err
	}
	if err := sftpClient.Chmod(tempPath, 0o700); err != nil {
		_ = sftpClient.Remove(tempPath)
		return "", err
	}
	if err := sftpClient.PosixRename(tempPath, remotePath); err != nil {
		_ = sftpClient.Remove(tempPath)
		return "", err
	}
	remoteSHA, err := hashRemoteFile(sftpClient, remotePath)
	if err != nil || remoteSHA != localSHA {
		_ = sftpClient.Remove(remotePath)
		return "", fmt.Errorf("远端校验器摘要校验失败")
	}
	return remotePath, nil
}

func hashRemoteFile(client *sftp.Client, filename string) (string, error) {
	file, err := client.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizeLinuxArch(value string) string {
	switch strings.TrimSpace(value) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return strings.TrimSpace(value)
	}
}

func runRemoteModelValidator(ctx context.Context, client *ssh.Client, validatorPath, archivePath string, maxExpanded int64, progress ModelValidationProgress) (ModelArchiveInspection, error, bool) {
	session, err := client.NewSession()
	if err != nil {
		return ModelArchiveInspection{}, err, false
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return ModelArchiveInspection{}, err, false
	}
	var stderr bytes.Buffer
	session.Stderr = &stderr
	command := shellQuote(validatorPath) + " model-validator --archive " + shellQuote(archivePath) + " --max-expanded " + fmt.Sprintf("%d", maxExpanded)
	if err := session.Start(command); err != nil {
		return ModelArchiveInspection{}, err, false
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Signal(ssh.SIGTERM)
			_ = session.Close()
		case <-done:
		}
	}()
	defer close(done)

	var inspection ModelArchiveInspection
	var structuredErr error
	gotResult := false
	gotHello := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var event modelValidatorEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return ModelArchiveInspection{}, fmt.Errorf("解析远端校验器输出失败: %w", err), false
		}
		switch event.Type {
		case "hello":
			if event.Protocol != modelValidatorProtocol {
				return ModelArchiveInspection{}, fmt.Errorf("远端校验器协议版本不兼容: %d", event.Protocol), false
			}
			gotHello = true
		case "progress":
			if progress != nil {
				progress(event.BytesRead, event.TotalBytes)
			}
		case "result":
			if event.Inspection == nil {
				return ModelArchiveInspection{}, errors.New("远端校验器结果为空"), false
			}
			inspection = *event.Inspection
			gotResult = true
		case "error":
			structuredErr = &domain.AppError{Code: event.ErrorCode, Message: event.Message}
		}
	}
	scanErr := scanner.Err()
	waitErr := session.Wait()
	if structuredErr != nil {
		return ModelArchiveInspection{}, structuredErr, true
	}
	if scanErr != nil {
		return ModelArchiveInspection{}, scanErr, false
	}
	if ctx.Err() != nil {
		return ModelArchiveInspection{}, ctx.Err(), false
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		return ModelArchiveInspection{}, errors.New(message), false
	}
	if !gotResult {
		return ModelArchiveInspection{}, errors.New("远端校验器未返回结果"), false
	}
	if !gotHello {
		return ModelArchiveInspection{}, errors.New("远端校验器未完成协议握手"), false
	}
	return inspection, nil, true
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
		if cleaned == "." {
			// GNU tar commonly writes a harmless leading "./" directory when
			// packaging the contents of the model directory. Ignore only that
			// directory placeholder; a regular file using the same path is invalid.
			if header.Typeflag == tar.TypeDir {
				continue
			}
			return result, unsafeArchiveError()
		}
		if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
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

func (e *Executor) DeployModelArchive(ctx context.Context, host domain.Host, password []byte, model domain.Model, uploadID string, inspection ModelArchiveInspection, emit EmitFunc) error {
	client, _, err := e.connectHost(ctx, host, password)
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

// CleanupCancelledModelDeploy removes only one deployment's staging data. It
// refuses to remove a committed target directory and succeeds only after all
// task-owned paths and the final target are confirmed absent.
func (e *Executor) CleanupCancelledModelDeploy(ctx context.Context, host domain.Host, password []byte, model domain.Model, uploadID string, emit EmitFunc) error {
	client, _, err := e.connectHost(ctx, host, password)
	if err != nil {
		return err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sftpClient.Close()
	if _, err := sftpClient.Stat(model.TargetDir); err == nil {
		return &domain.AppError{Code: "MODEL_TASK_ALREADY_COMMITTED", Message: "模型目标目录已经存在，已拒绝将停止操作伪装为成功"}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("确认模型目标目录失败: %w", err)
	}
	tempDir := path.Join(path.Dir(model.TargetDir), ".dp-model-extract-"+uploadID)
	if emit != nil {
		emit("system", "正在删除任务专属解压目录和远端压缩包")
	}
	if _, err := runCommand(ctx, client, "rm -rf -- "+shellQuote(tempDir), password, emit); err != nil {
		return fmt.Errorf("删除任务专属解压目录失败: %w", err)
	}
	archivePath := ModelUploadRemotePath(model.TargetDir, uploadID)
	if err := sftpClient.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除远端模型压缩包失败: %w", err)
	}
	checks := []struct{ label, target string }{
		{"模型目标目录", model.TargetDir},
		{"任务解压目录", tempDir},
		{"远端压缩包", archivePath},
	}
	for _, check := range checks {
		if _, err := sftpClient.Stat(check.target); err == nil {
			return fmt.Errorf("%s清理后仍然存在", check.label)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("确认%s清理结果失败: %w", check.label, err)
		}
	}
	if emit != nil {
		emit("system", "清理完成，已确认模型目标目录不存在")
	}
	return nil
}

func (e *Executor) DeleteModel(ctx context.Context, host domain.Host, password []byte, model domain.Model, taskID string, emit EmitFunc) error {
	client, _, err := e.connectHost(ctx, host, password)
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

func (e *Executor) ModelTargetOwned(ctx context.Context, host domain.Host, password []byte, model domain.Model) (bool, error) {
	client, _, err := e.connectHost(ctx, host, password)
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
