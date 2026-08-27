package remote

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"DP/internal/domain"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	installedMarker  = ".dp-installed.json"
	installingMarker = ".dp-installing.json"
)

type EmitFunc func(stream, message string)

type ValidationResult struct {
	Fingerprint string            `json:"fingerprint"`
	Arch        string            `json:"arch"`
	Stages      []ValidationStage `json:"stages"`
}

type ValidationStage struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type Executor struct {
	connectTimeout time.Duration
	scriptTimeout  time.Duration
	uploadTimeout  time.Duration
}

func NewExecutor(uploadTimeout time.Duration) *Executor {
	return &Executor{
		connectTimeout: 10 * time.Second,
		scriptTimeout:  3 * time.Minute,
		uploadTimeout:  uploadTimeout,
	}
}

func (e *Executor) Validate(ctx context.Context, env domain.Environment, password []byte) (result ValidationResult, err error) {
	client, fingerprint, err := e.connect(ctx, env, password)
	if err != nil {
		result.Stages = append(result.Stages, ValidationStage{
			Name: "connect", Success: false, Message: userRemoteError(err),
		})
		return result, err
	}
	defer client.Close()
	result.Fingerprint = fingerprint
	result.Stages = append(result.Stages, ValidationStage{
		Name: "connect", Success: true, Message: "SSH 连接成功",
	})

	// 架构采集为尽力而为：失败不追加失败 stage，也不影响校验结果。
	if arch, archErr := detectArch(client); archErr == nil {
		result.Arch = arch
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		result.Stages = append(result.Stages, ValidationStage{
			Name: "directory", Success: false, Message: "目标服务器未提供可用的 SFTP 服务",
		})
		return result, fmt.Errorf("start SFTP: %w", err)
	}
	defer sftpClient.Close()
	if err := sftpClient.MkdirAll(env.InstallDir); err != nil {
		result.Stages = append(result.Stages, ValidationStage{
			Name: "directory", Success: false, Message: "安装目录无法创建",
		})
		return result, fmt.Errorf("create install directory: %w", err)
	}
	result.Stages = append(result.Stages, ValidationStage{
		Name: "directory", Success: true, Message: "安装目录可用",
	})

	testName := path.Join(env.InstallDir, ".dp-write-test-"+randomSuffix())
	testFile, err := sftpClient.OpenFile(testName, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		result.Stages = append(result.Stages, ValidationStage{
			Name: "upload", Success: false, Message: "安装目录没有上传权限",
		})
		return result, fmt.Errorf("create write test: %w", err)
	}
	_, writeErr := testFile.Write([]byte("dp"))
	closeErr := testFile.Close()
	_ = sftpClient.Remove(testName)
	if writeErr != nil || closeErr != nil {
		result.Stages = append(result.Stages, ValidationStage{
			Name: "upload", Success: false, Message: "测试文件写入失败",
		})
		return result, errors.Join(writeErr, closeErr)
	}
	result.Stages = append(result.Stages, ValidationStage{
		Name: "upload", Success: true, Message: "文件上传权限正常",
	})
	return result, nil
}

// DetectArch 通过 SSH 在目标服务器执行 uname -m 获取 CPU 架构，供安装流程使用。
func (e *Executor) DetectArch(ctx context.Context, env domain.Environment, password []byte) (string, error) {
	client, _, err := e.connect(ctx, env, password)
	if err != nil {
		return "", err
	}
	defer client.Close()
	return detectArch(client)
}

func detectArch(client *ssh.Client) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()
	output, err := session.Output("uname -m")
	if err != nil {
		return "", fmt.Errorf("run uname -m: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (e *Executor) Install(
	ctx context.Context,
	env domain.Environment,
	password []byte,
	packagePath, packageSHA string,
	healthPort int,
	configPath string,
	configContent []byte,
	hasInstall bool,
	stripRoot bool,
	emit EmitFunc,
) (string, int, error) {
	client, fingerprint, err := e.connect(ctx, env, password)
	if err != nil {
		return "", 0, err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fingerprint, 0, fmt.Errorf("start SFTP: %w", err)
	}
	defer sftpClient.Close()
	if err := sftpClient.MkdirAll(env.InstallDir); err != nil {
		return fingerprint, 0, fmt.Errorf("create install directory: %w", err)
	}

	installedPath := path.Join(env.InstallDir, installedMarker)
	if _, err := sftpClient.Stat(installedPath); err == nil {
		existing := &domain.ExistingInstallationError{}
		if markerFile, openErr := sftpClient.Open(installedPath); openErr == nil {
			var marker struct {
				PackageSHA256 string `json:"package_sha256"`
				HealthPort    int    `json:"health_port"`
			}
			if decodeErr := json.NewDecoder(io.LimitReader(markerFile, 1<<20)).Decode(&marker); decodeErr == nil {
				existing.PackageSHA256 = marker.PackageSHA256
				existing.HealthPort = marker.HealthPort
			}
			_ = markerFile.Close()
		}
		return fingerprint, 0, existing
	} else if !os.IsNotExist(err) {
		return fingerprint, 0, fmt.Errorf("inspect installed marker: %w", err)
	}
	retry := false
	if _, err := sftpClient.Stat(path.Join(env.InstallDir, installingMarker)); err == nil {
		retry = true
	}
	if !retry {
		entries, err := sftpClient.ReadDir(env.InstallDir)
		if err != nil {
			return fingerprint, 0, fmt.Errorf("inspect install directory: %w", err)
		}
		if len(entries) > 0 {
			return fingerprint, 0, &domain.AppError{
				Code: "INSTALL_DIR_NOT_EMPTY", Message: "安装目录包含未知文件，已拒绝覆盖",
			}
		}
		marker := map[string]any{
			"service_type": env.ServiceType, "started_at": time.Now().UTC(),
			"package_sha256": packageSHA,
		}
		if err := writeJSONFile(sftpClient, path.Join(env.InstallDir, installingMarker), marker); err != nil {
			return fingerprint, 0, fmt.Errorf("write installing marker: %w", err)
		}
		emit("system", "已创建安装事务标记")
	} else {
		emit("system", "检测到上次未完成的安装，允许重试")
	}

	remotePackage := path.Join(env.InstallDir, ".dp-package-"+randomSuffix()+".tar.gz")
	defer sftpClient.Remove(remotePackage)
	emit("system", "开始上传安装包")
	if err := e.upload(ctx, sftpClient, packagePath, remotePackage, emit); err != nil {
		return fingerprint, 0, err
	}
	emit("system", "安装包上传完成")

	extractCtx, cancelExtract := context.WithTimeout(ctx, time.Minute)
	extractCommand := buildExtractCommand(remotePackage, env.InstallDir, stripRoot)
	if stripRoot {
		emit("system", "检测到安装包单层根目录，解压时自动剥离")
	}
	exitCode, err := runCommand(extractCtx, client, extractCommand, password, emit)
	cancelExtract()
	if err != nil {
		return fingerprint, exitCode, fmt.Errorf("解压安装包失败: %w", err)
	}
	emit("system", "安装包解压完成")
	if err := writeSFTPConfig(sftpClient, path.Join(env.InstallDir, configPath), configContent); err != nil {
		return fingerprint, 0, err
	}
	emit("system", "已写入当前服务实例配置")

	script := "start.sh"
	if hasInstall {
		script = "install.sh"
	}
	emit("system", "开始执行 "+script)
	exitCode, err = e.runScript(ctx, client, env.InstallDir, script, password, emit)
	if err != nil {
		return fingerprint, exitCode, err
	}

	marker := map[string]any{
		"service_type": env.ServiceType, "installed_at": time.Now().UTC(),
		"package_sha256": packageSHA, "health_port": healthPort,
	}
	if err := writeJSONFile(sftpClient, installedPath, marker); err != nil {
		return fingerprint, exitCode, fmt.Errorf("write installed marker: %w", err)
	}
	_ = sftpClient.Remove(path.Join(env.InstallDir, installingMarker))
	return fingerprint, exitCode, nil
}

func (e *Executor) RunScript(
	ctx context.Context,
	env domain.Environment,
	password []byte,
	script string,
	emit EmitFunc,
) (string, int, error) {
	client, fingerprint, err := e.connect(ctx, env, password)
	if err != nil {
		return "", 0, err
	}
	defer client.Close()
	emit("system", "开始执行 "+script)
	exitCode, err := e.runScript(ctx, client, env.InstallDir, script, password, emit)
	return fingerprint, exitCode, err
}

// FollowComposeLogs streams the Docker Compose project in the service install directory
// until the caller cancels the context or the remote command exits.
func (e *Executor) FollowComposeLogs(
	ctx context.Context,
	env domain.Environment,
	password []byte,
	tail int,
	emit EmitFunc,
) (string, error) {
	client, fingerprint, err := e.connect(ctx, env, password)
	if err != nil {
		return "", err
	}
	defer client.Close()
	command := buildComposeLogCommand(env.InstallDir, tail)
	_, err = runCommand(ctx, client, command, password, emit)
	return fingerprint, err
}

func (e *Executor) ResetInstallation(
	ctx context.Context,
	env domain.Environment,
	password []byte,
	runStop bool,
	emit EmitFunc,
) (string, int, error) {
	client, fingerprint, err := e.connect(ctx, env, password)
	if err != nil {
		return "", 0, err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fingerprint, 0, fmt.Errorf("start SFTP: %w", err)
	}
	defer sftpClient.Close()
	exitCode := 0
	if runStop {
		// 安装失败后远端可能从未部署 stop.sh，缺失时跳过而不是报错。
		if _, statErr := sftpClient.Stat(path.Join(env.InstallDir, "stop.sh")); statErr == nil {
			emit("system", "重置前开始执行 stop.sh")
			exitCode, err = e.runScript(ctx, client, env.InstallDir, "stop.sh", password, emit)
			if err != nil {
				return fingerprint, exitCode, err
			}
			emit("system", "stop.sh 执行成功")
		} else {
			emit("system", "远端不存在 stop.sh，跳过停止脚本")
		}
	} else {
		emit("system", "检测到最近一次成功操作为停止，跳过 stop.sh")
	}

	// 未安装过的环境远端目录可能不存在，写标记前确保目录可用。
	if err := sftpClient.MkdirAll(env.InstallDir); err != nil {
		return fingerprint, exitCode, fmt.Errorf("create install directory: %w", err)
	}
	installedPath := path.Join(env.InstallDir, installedMarker)
	if err := sftpClient.Remove(installedPath); err != nil && !os.IsNotExist(err) {
		return fingerprint, exitCode, fmt.Errorf("remove installed marker: %w", err)
	}
	installingPath := path.Join(env.InstallDir, installingMarker)
	_ = sftpClient.Remove(installingPath)
	marker := map[string]any{
		"service_type": env.ServiceType,
		"reset_at":     time.Now().UTC(),
		"reason":       "manual_reset",
	}
	if err := writeJSONFile(sftpClient, installingPath, marker); err != nil {
		return fingerprint, exitCode, fmt.Errorf("write reinstall marker: %w", err)
	}
	emit("system", "远端安装标记已重置，原目录文件未删除")
	return fingerprint, exitCode, nil
}

func (e *Executor) runScript(
	ctx context.Context,
	client *ssh.Client,
	installDir, script string,
	password []byte,
	emit EmitFunc,
) (int, error) {
	scriptCtx, cancel := context.WithTimeout(ctx, e.scriptTimeout)
	defer cancel()
	exitCode, err := runCommand(scriptCtx, client,
		"cd "+shellQuote(installDir)+" && ./"+script, password, emit)
	if errors.Is(err, context.DeadlineExceeded) {
		return exitCode, domain.ErrTimedOut
	}
	return exitCode, err
}

func (e *Executor) WriteConfig(
	ctx context.Context,
	env domain.Environment,
	password []byte,
	configPath string,
	content []byte,
) (string, error) {
	client, fingerprint, err := e.connect(ctx, env, password)
	if err != nil {
		return "", err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fingerprint, fmt.Errorf("start SFTP: %w", err)
	}
	defer sftpClient.Close()
	target := path.Join(env.InstallDir, configPath)
	if err := sftpClient.MkdirAll(path.Dir(target)); err != nil {
		return fingerprint, fmt.Errorf("create config directory: %w", err)
	}
	if err := writeSFTPConfig(sftpClient, target, content); err != nil {
		return fingerprint, err
	}
	return fingerprint, nil
}

func writeSFTPConfig(sftpClient *sftp.Client, target string, content []byte) error {
	temp := target + ".dp-tmp-" + randomSuffix()
	file, err := sftpClient.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	if err != nil {
		return fmt.Errorf("create remote config: %w", err)
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = sftpClient.Remove(temp)
		return fmt.Errorf("write remote config: %w", errors.Join(writeErr, closeErr))
	}
	if err := sftpClient.PosixRename(temp, target); err != nil {
		_ = sftpClient.Remove(temp)
		return fmt.Errorf("replace remote config: %w", err)
	}
	return nil
}

func (e *Executor) connect(
	ctx context.Context,
	env domain.Environment,
	password []byte,
) (*ssh.Client, string, error) {
	dialer := net.Dialer{Timeout: e.connectTimeout}
	address := net.JoinHostPort(env.IP, fmt.Sprintf("%d", env.SSHPort))
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, "", fmt.Errorf("connect SSH: %w", err)
	}
	var observed string
	callback := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		observed = ssh.FingerprintSHA256(key)
		if env.HostKeyFingerprint != "" && env.HostKeyFingerprint != observed {
			return fmt.Errorf("SSH 主机指纹已变化（期望 %s，实际 %s）", env.HostKeyFingerprint, observed)
		}
		return nil
	}
	config := &ssh.ClientConfig{
		User:            env.SSHUser,
		Auth:            []ssh.AuthMethod{ssh.Password(string(password))},
		HostKeyCallback: callback,
		Timeout:         e.connectTimeout,
	}
	conn, channels, requests, err := ssh.NewClientConn(connection, address, config)
	if err != nil {
		_ = connection.Close()
		return nil, observed, fmt.Errorf("SSH authentication or handshake failed: %w", err)
	}
	return ssh.NewClient(conn, channels, requests), observed, nil
}

func (e *Executor) upload(
	ctx context.Context,
	client *sftp.Client,
	localPath, remotePath string,
	emit EmitFunc,
) error {
	uploadCtx, cancel := context.WithTimeout(ctx, e.uploadTimeout)
	defer cancel()
	local, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local package: %w", err)
	}
	defer local.Close()
	info, err := local.Stat()
	if err != nil {
		return err
	}
	remote, err := client.OpenFile(remotePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	if err != nil {
		return fmt.Errorf("create remote package: %w", err)
	}
	defer remote.Close()

	reader := &contextReader{ctx: uploadCtx, r: local}
	progress := &progressWriter{w: remote, total: info.Size(), emit: emit}
	_, err = io.Copy(progress, reader)
	if closeErr := remote.Close(); err == nil {
		err = closeErr
	}
	if errors.Is(uploadCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("upload package: %w", domain.ErrTimedOut)
	}
	if err != nil {
		return fmt.Errorf("upload package: %w", err)
	}
	return nil
}

func runCommand(
	ctx context.Context,
	client *ssh.Client,
	command string,
	password []byte,
	emit EmitFunc,
) (int, error) {
	session, err := client.NewSession()
	if err != nil {
		return 0, fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return 0, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return 0, err
	}
	if err := session.Start(command); err != nil {
		return 0, fmt.Errorf("start remote command: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go streamLines(stdout, "stdout", password, emit, &wg)
	go streamLines(stderr, "stderr", password, emit, &wg)
	waitCh := make(chan error, 1)
	go func() { waitCh <- session.Wait() }()

	var waitErr error
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		waitErr = ctx.Err()
	case waitErr = <-waitCh:
	}
	wg.Wait()
	if waitErr == nil {
		return 0, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitStatus(), fmt.Errorf("remote command exited with code %d", exitErr.ExitStatus())
	}
	return -1, waitErr
}

func streamLines(reader io.Reader, stream string, password []byte, emit EmitFunc, wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		message := scanner.Text()
		if len(password) > 0 {
			message = strings.ReplaceAll(message, string(password), "******")
		}
		emit(stream, message)
	}
	if err := scanner.Err(); err != nil {
		emit("system", stream+" 读取失败: "+err.Error())
	}
}

func writeJSONFile(client *sftp.Client, filename string, value any) error {
	content, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp := filename + ".tmp-" + randomSuffix()
	file, err := client.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = client.Remove(temp)
		return errors.Join(writeErr, closeErr)
	}
	if err := client.Rename(temp, filename); err != nil {
		_ = client.Remove(temp)
		return err
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func buildExtractCommand(remotePackage, installDir string, stripRoot bool) string {
	command := "tar -xzf " + shellQuote(remotePackage) + " -C " + shellQuote(installDir)
	if stripRoot {
		command += " --strip-components=1"
	}
	return command
}

func buildComposeLogCommand(installDir string, tail int) string {
	return "cd " + shellQuote(installDir) +
		" && exec docker compose --ansi always logs --follow --tail " +
		fmt.Sprintf("%d", tail) + " --timestamps"
}

func randomSuffix() string {
	return strings.ReplaceAll(fmt.Sprintf("%d", time.Now().UnixNano()), "-", "")
}

func userRemoteError(err error) string {
	message := err.Error()
	if strings.Contains(message, "unable to authenticate") ||
		strings.Contains(message, "authentication") {
		return "SSH 用户名或密码错误"
	}
	if strings.Contains(message, "fingerprint") || strings.Contains(message, "主机指纹") {
		return message
	}
	return "SSH 连接失败"
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}

type progressWriter struct {
	w       io.Writer
	total   int64
	written int64
	next    int
	emit    EmitFunc
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.written += int64(n)
	if w.total > 0 {
		percent := int(w.written * 100 / w.total)
		if percent >= w.next {
			w.emit("system", fmt.Sprintf("上传进度 %d%%", percent))
			w.next = (percent/10 + 1) * 10
		}
	}
	return n, err
}
