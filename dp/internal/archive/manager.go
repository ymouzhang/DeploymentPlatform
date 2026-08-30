package archive

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
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"DP/internal/domain"
	"DP/internal/store"

	"go.yaml.in/yaml/v3"
)

const (
	maxFiles  = 100_000
	maxConfig = 10 << 20
	maxScript = 1 << 20
)

// configCandidates 是安装包允许携带的配置文件路径，必须恰好存在其中一个。
var configCandidates = []struct {
	path       string
	configType string
}{
	{path: "config/config.json", configType: "json"},
	{path: "config/config.yaml", configType: "yaml"},
	{path: "config/application.yml", configType: "yaml"},
	{path: "config/application.yaml", configType: "yaml"},
}

const configCandidatesHint = "config/config.json、config/config.yaml、config/application.yml 或 config/application.yaml"

type Manager struct {
	dataDir          string
	maxArchiveBytes  int64
	maxExpanded      int64
	store            *store.Store
	versionRetention int
	mu               sync.RWMutex
}

type Inspection struct {
	Port       int
	HasInstall bool
	HasStart   bool
	HasStop    bool
	Config     []byte
	ConfigPath string
	ConfigType string
	RootPrefix string
}

func NewManager(dataDir string, maxArchiveBytes int64, store *store.Store) *Manager {
	maxExpanded := maxArchiveBytes * 10
	if maxExpanded < maxArchiveBytes {
		maxExpanded = maxArchiveBytes
	}
	return &Manager{
		dataDir: dataDir, maxArchiveBytes: maxArchiveBytes,
		maxExpanded: maxExpanded, store: store, versionRetention: 10,
	}
}

func (m *Manager) ConfigureRetention(retain int) {
	if retain > 0 {
		m.versionRetention = retain
	}
}

// Upload 上传或替换安装包。note 为 nil 时保留已有备注（新上传则为空），否则使用传入备注。
func (m *Manager) Upload(
	ctx context.Context, serviceType, filename string, src io.Reader, note *string,
) (domain.Package, error) {
	return m.UploadForOwner(ctx, store.InitialAdminID, serviceType, filename, src, note)
}

func (m *Manager) UploadForOwner(
	ctx context.Context, ownerID, serviceType, filename string, src io.Reader, note *string,
) (domain.Package, error) {
	return m.UploadVersionForOwner(ctx, ownerID, serviceType, filename, src, note, domain.User{ID: ownerID})
}

func (m *Manager) UploadVersionForOwner(
	ctx context.Context, ownerID, serviceType, filename string, src io.Reader, note *string, uploader domain.User,
) (domain.Package, error) {
	serviceType = strings.ToLower(strings.TrimSpace(serviceType))
	if err := domain.ValidateServiceType(serviceType); err != nil {
		return domain.Package{}, err
	}
	if note != nil {
		if err := domain.ValidateNote(*note); err != nil {
			return domain.Package{}, err
		}
	}
	filename = filepath.Base(filename)
	if !strings.HasSuffix(strings.ToLower(filename), ".tar.gz") {
		return domain.Package{}, domain.FieldError("file", "安装包仅支持 .tar.gz 格式")
	}

	tempDir := filepath.Join(m.dataDir, "tmp")
	if err := os.MkdirAll(tempDir, 0o750); err != nil {
		return domain.Package{}, err
	}
	temp, err := os.CreateTemp(tempDir, "package-*.tar.gz")
	if err != nil {
		return domain.Package{}, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	hash := sha256.New()
	limited := io.LimitReader(src, m.maxArchiveBytes+1)
	written, err := io.Copy(io.MultiWriter(temp, hash), limited)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return domain.Package{}, fmt.Errorf("保存上传文件失败: %w", err)
	}
	if written > m.maxArchiveBytes {
		return domain.Package{}, domain.FieldError("file", "安装包超过允许大小")
	}
	if written < 2 {
		return domain.Package{}, domain.FieldError("file", "安装包为空")
	}

	inspection, err := inspect(tempPath, m.maxExpanded, true)
	if err != nil {
		return domain.Package{}, err
	}
	if !inspection.HasStart || !inspection.HasStop {
		return domain.Package{}, domain.FieldError("file", "安装包根目录必须包含 start.sh 和 stop.sh")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var existingNote string
	if currentPackage, getErr := m.store.GetPackageByOwner(ctx, ownerID, serviceType); getErr == nil {
		existingNote = currentPackage.Note
		current, inspectErr := inspect(m.AbsolutePath(currentPackage), m.maxExpanded, false)
		if inspectErr != nil {
			return domain.Package{}, inspectErr
		}
		if current.ConfigType != inspection.ConfigType ||
			RelativeConfigPath(current) != RelativeConfigPath(inspection) {
			return domain.Package{}, domain.FieldError(
				"file",
				"替换安装包不能改变配置文件格式或路径，以免已有服务实例配置失效",
			)
		}
	} else if !errors.Is(getErr, domain.ErrNotFound) {
		return domain.Package{}, getErr
	}

	versionID := store.NewID()
	relative := filepath.Join("packages", ownerID, serviceType, "versions", versionID+".tar.gz")
	destination := filepath.Join(m.dataDir, relative)
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return domain.Package{}, err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return domain.Package{}, fmt.Errorf("保存安装包版本失败: %w", err)
	}
	now := time.Now().UTC()
	pkgNote := existingNote
	if note != nil {
		pkgNote = *note
	}
	version := domain.PackageVersion{
		ID: versionID, OwnerID: ownerID, ServiceType: serviceType, OriginalFilename: filename,
		StoragePath: relative, SHA256: hex.EncodeToString(hash.Sum(nil)), SizeBytes: written,
		ConfigPort: inspection.Port, ConfigFormat: inspection.ConfigType,
		ConfigPath: RelativeConfigPath(inspection), Note: pkgNote,
		UploadedBy: uploader.ID, UploadedByName: uploader.Username, UploadedAt: now,
	}
	if err := m.store.SavePackageVersion(ctx, version); err != nil {
		_ = os.Remove(destination)
		return domain.Package{}, err
	}
	m.pruneVersions(ctx, ownerID, serviceType)
	return m.store.GetPackageByOwner(ctx, ownerID, serviceType)
}

// UpdateNote 仅更新安装包备注，包文件保持不变。
func (m *Manager) UpdateNote(ctx context.Context, serviceType, note string) (domain.Package, error) {
	return m.UpdateNoteForOwner(ctx, store.InitialAdminID, serviceType, note)
}

func (m *Manager) UpdateNoteForOwner(ctx context.Context, ownerID, serviceType, note string) (domain.Package, error) {
	serviceType = strings.ToLower(strings.TrimSpace(serviceType))
	if err := domain.ValidateServiceType(serviceType); err != nil {
		return domain.Package{}, err
	}
	if err := domain.ValidateNote(note); err != nil {
		return domain.Package{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	pkg, err := m.store.GetPackageByOwner(ctx, ownerID, serviceType)
	if err != nil {
		return domain.Package{}, err
	}
	if pkg.CurrentVersionID != "" {
		if err := m.store.UpdatePackageVersionNote(ctx, ownerID, serviceType, pkg.CurrentVersionID, note); err != nil {
			return domain.Package{}, err
		}
		return m.store.GetPackageByOwner(ctx, ownerID, serviceType)
	}
	return m.store.UpdatePackageNoteByOwner(ctx, ownerID, serviceType, note)
}

func (m *Manager) ListVersions(ctx context.Context, ownerID, serviceType string) ([]domain.PackageVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, err := m.store.GetPackageByOwner(ctx, ownerID, serviceType); err != nil {
		return nil, err
	}
	return m.store.ListPackageVersions(ctx, ownerID, serviceType)
}

func (m *Manager) ActivateVersion(ctx context.Context, ownerID, serviceType, versionID string) (domain.Package, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.store.GetPackageByOwner(ctx, ownerID, serviceType)
	if err != nil {
		return domain.Package{}, err
	}
	target, err := m.store.GetPackageVersion(ctx, ownerID, serviceType, versionID)
	if err != nil {
		return domain.Package{}, err
	}
	if target.Current {
		return current, nil
	}
	currentInspection, err := inspect(m.AbsolutePath(current), m.maxExpanded, false)
	if err != nil {
		return domain.Package{}, err
	}
	targetInspection, err := inspect(filepath.Join(m.dataDir, filepath.Clean(target.StoragePath)), m.maxExpanded, false)
	if err != nil {
		return domain.Package{}, err
	}
	if currentInspection.ConfigType != targetInspection.ConfigType || RelativeConfigPath(currentInspection) != RelativeConfigPath(targetInspection) {
		return domain.Package{}, &domain.AppError{Code: "PACKAGE_VERSION_INCOMPATIBLE", Message: "历史版本的配置格式或路径与当前版本不兼容"}
	}
	if err := m.store.ActivatePackageVersion(ctx, target); err != nil {
		return domain.Package{}, err
	}
	return m.store.GetPackageByOwner(ctx, ownerID, serviceType)
}

func (m *Manager) DeleteVersion(ctx context.Context, ownerID, serviceType, versionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteVersionLocked(ctx, ownerID, serviceType, versionID)
}

func (m *Manager) deleteVersionLocked(ctx context.Context, ownerID, serviceType, versionID string) error {
	version, err := m.store.GetPackageVersion(ctx, ownerID, serviceType, versionID)
	if err != nil {
		return err
	}
	if version.Current {
		return &domain.AppError{Code: "PACKAGE_VERSION_CURRENT", Message: "当前版本不能删除，请先切换其他版本"}
	}
	if version.ReferencedCount > 0 {
		return &domain.AppError{Code: "PACKAGE_VERSION_IN_USE", Message: "该版本仍被环境引用，不能删除"}
	}
	path := filepath.Join(m.dataDir, filepath.Clean(version.StoragePath))
	trash := path + ".deleting"
	if err := os.Rename(path, trash); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := m.store.DeletePackageVersion(ctx, ownerID, serviceType, versionID); err != nil {
		_ = os.Rename(trash, path)
		return err
	}
	if err := os.Remove(trash); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除安装包版本文件失败: %w", err)
	}
	return nil
}

func (m *Manager) pruneVersions(ctx context.Context, ownerID, serviceType string) {
	items, err := m.store.PrunablePackageVersions(ctx, ownerID, serviceType, m.versionRetention)
	if err != nil {
		return
	}
	for _, item := range items {
		if err := m.deleteVersionLocked(ctx, ownerID, serviceType, item.ID); err != nil {
			continue
		}
	}
}

func (m *Manager) Get(ctx context.Context, serviceType string) (domain.Package, error) {
	return m.GetForOwner(ctx, store.InitialAdminID, serviceType)
}

func (m *Manager) GetForOwner(ctx context.Context, ownerID, serviceType string) (domain.Package, error) {
	return m.store.GetPackageByOwner(ctx, ownerID, serviceType)
}

func (m *Manager) Delete(ctx context.Context, serviceType string) error {
	return m.DeleteForOwner(ctx, store.InitialAdminID, serviceType)
}

func (m *Manager) DeleteForOwner(ctx context.Context, ownerID, serviceType string) error {
	serviceType = strings.ToLower(strings.TrimSpace(serviceType))
	if err := domain.ValidateServiceType(serviceType); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, err := m.store.GetPackageByOwner(ctx, ownerID, serviceType)
	if err != nil {
		return err
	}
	versions, err := m.store.ListPackageVersions(ctx, ownerID, serviceType)
	if err != nil {
		return err
	}
	installed, err := m.store.CountInstalledEnvironmentsByOwner(ctx, ownerID, serviceType)
	if err != nil {
		return err
	}
	if installed > 0 {
		return &domain.AppError{
			Code:    "PACKAGE_IN_USE",
			Message: "该服务类型存在已安装的环境，请先重置后再删除安装包",
		}
	}
	if err := m.store.DeletePackageByOwner(ctx, ownerID, serviceType); err != nil {
		return err
	}
	var removeErr error
	for _, version := range versions {
		path := filepath.Join(m.dataDir, filepath.Clean(version.StoragePath))
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErr = errors.Join(removeErr, err)
		}
	}
	if removeErr != nil {
		return fmt.Errorf("删除安装包文件失败: %w", removeErr)
	}
	return nil
}

// TransferOwner changes business ownership without moving immutable package files.
func (m *Manager) TransferOwner(ctx context.Context, sourceID, targetID string) (domain.TransferResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.TransferResources(ctx, sourceID, targetID)
}

func (m *Manager) AbsolutePath(pkg domain.Package) string {
	return filepath.Join(m.dataDir, filepath.Clean(pkg.StoragePath))
}

func (m *Manager) Snapshot(
	ctx context.Context,
	serviceType string,
) (domain.Package, string, Inspection, func(), error) {
	return m.SnapshotForOwner(ctx, store.InitialAdminID, serviceType)
}

func (m *Manager) SnapshotForOwner(
	ctx context.Context, ownerID, serviceType string,
) (domain.Package, string, Inspection, func(), error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pkg, err := m.store.GetPackageByOwner(ctx, ownerID, serviceType)
	if err != nil {
		return domain.Package{}, "", Inspection{}, func() {}, err
	}
	source, err := os.Open(m.AbsolutePath(pkg))
	if err != nil {
		return domain.Package{}, "", Inspection{}, func() {}, err
	}
	defer source.Close()
	tempDir := filepath.Join(m.dataDir, "tmp")
	if err := os.MkdirAll(tempDir, 0o750); err != nil {
		return domain.Package{}, "", Inspection{}, func() {}, err
	}
	temp, err := os.CreateTemp(tempDir, "snapshot-*.tar.gz")
	if err != nil {
		return domain.Package{}, "", Inspection{}, func() {}, err
	}
	tempPath := temp.Name()
	cleanup := func() { _ = os.Remove(tempPath) }
	if _, err = io.Copy(temp, source); err == nil {
		err = temp.Close()
	} else {
		_ = temp.Close()
	}
	if err != nil {
		cleanup()
		return domain.Package{}, "", Inspection{}, func() {}, err
	}
	inspection, err := inspect(tempPath, m.maxExpanded, true)
	if err != nil {
		cleanup()
		return domain.Package{}, "", Inspection{}, func() {}, err
	}
	return pkg, tempPath, inspection, cleanup, nil
}

func (m *Manager) ReadConfig(
	ctx context.Context,
	serviceType string,
) ([]byte, domain.Package, Inspection, error) {
	return m.ReadConfigForOwner(ctx, store.InitialAdminID, serviceType)
}

func (m *Manager) ReadConfigForOwner(
	ctx context.Context, ownerID, serviceType string,
) ([]byte, domain.Package, Inspection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pkg, err := m.store.GetPackageByOwner(ctx, ownerID, serviceType)
	if err != nil {
		return nil, domain.Package{}, Inspection{}, err
	}
	inspection, err := inspect(m.AbsolutePath(pkg), m.maxExpanded, true)
	if err != nil {
		return nil, domain.Package{}, Inspection{}, err
	}
	return inspection.Config, pkg, inspection, nil
}

func InspectForInstall(filename string, maxExpanded int64) (Inspection, error) {
	return inspect(filename, maxExpanded, false)
}

func ValidateConfig(content []byte, configType string) (int, error) {
	if len(content) == 0 || len(content) > maxConfig {
		return 0, domain.FieldError("content", "配置文件为空或过大")
	}
	return configPort(content, configType)
}

func RelativeConfigPath(inspection Inspection) string {
	return strings.TrimPrefix(inspection.ConfigPath, inspection.RootPrefix+"/")
}

func inspect(filename string, maxExpanded int64, includeConfig bool) (Inspection, error) {
	file, err := os.Open(filename)
	if err != nil {
		return Inspection{}, err
	}
	defer file.Close()
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return Inspection{}, domain.FieldError("file", "文件不是有效的 gzip 压缩包")
	}
	defer gzReader.Close()
	tarReader := tar.NewReader(gzReader)
	var result Inspection
	var expanded int64
	var files int
	var configSeen bool
	archivePaths := make([]string, 0, 64)
	regularModes := make(map[string]int64)
	scriptContents := make(map[string][]byte)

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Inspection{}, domain.FieldError("file", "tar.gz 内容损坏")
		}
		files++
		if files > maxFiles {
			return Inspection{}, domain.FieldError("file", "安装包文件数量过多")
		}
		if header.Typeflag == tar.TypeDir && path.Clean(header.Name) == "." {
			continue
		}
		clean := cleanArchivePath(header.Name)
		if clean == "" {
			return Inspection{}, domain.FieldError("file", "安装包包含不安全路径")
		}
		archivePaths = append(archivePaths, clean)
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 {
				return Inspection{}, domain.FieldError("file", "安装包包含非法文件大小")
			}
			expanded += header.Size
			if expanded > maxExpanded {
				return Inspection{}, domain.FieldError("file", "安装包解压后内容超过允许大小")
			}
			regularModes[clean] = header.Mode
		case tar.TypeDir:
			continue
		default:
			return Inspection{}, domain.FieldError("file", "安装包不允许包含链接、设备或其他特殊文件")
		}
		if rootPrefix, configType, isConfig := matchConfigPath(clean); isConfig {
			if configSeen {
				return Inspection{}, domain.FieldError(
					"file",
					"安装包不能同时包含多个配置文件（"+configCandidatesHint+" 任选其一）",
				)
			}
			if header.Size > maxConfig {
				return Inspection{}, domain.FieldError("file", "配置文件过大")
			}
			content, err := io.ReadAll(io.LimitReader(tarReader, maxConfig+1))
			if err != nil {
				return Inspection{}, err
			}
			port, err := configPort(content, configType)
			if err != nil {
				return Inspection{}, err
			}
			result.Port = port
			result.ConfigPath = clean
			result.ConfigType = configType
			result.RootPrefix = rootPrefix
			configSeen = true
			if includeConfig {
				result.Config = content
			}
		} else if _, _, isScript := matchRootFile(clean, "start.sh"); isScript {
			if header.Size > maxScript {
				return Inspection{}, domain.FieldError("file", "start.sh 文件过大")
			}
			content, err := io.ReadAll(io.LimitReader(tarReader, maxScript+1))
			if err != nil {
				return Inspection{}, err
			}
			scriptContents[clean] = content
		}
	}
	if !configSeen {
		return Inspection{}, domain.FieldError(
			"file",
			"安装包中缺少配置文件（需要 "+configCandidatesHint+" 其中之一）",
		)
	}
	if result.RootPrefix != "" {
		for _, name := range archivePaths {
			if name != result.RootPrefix && !strings.HasPrefix(name, result.RootPrefix+"/") {
				return Inspection{}, domain.FieldError(
					"file",
					"安装包使用单层根目录时，所有文件必须位于同一目录下",
				)
			}
		}
	}
	scriptPath := func(name string) string {
		if result.RootPrefix == "" {
			return name
		}
		return path.Join(result.RootPrefix, name)
	}
	if mode, ok := regularModes[scriptPath("install.sh")]; ok {
		if mode&0o111 == 0 {
			return Inspection{}, domain.FieldError("file", "install.sh 必须具有可执行权限")
		}
		result.HasInstall = true
	}
	if mode, ok := regularModes[scriptPath("start.sh")]; ok {
		if mode&0o111 == 0 {
			return Inspection{}, domain.FieldError("file", "start.sh 必须具有可执行权限")
		}
		result.HasStart = true
	}
	if mode, ok := regularModes[scriptPath("stop.sh")]; ok {
		if mode&0o111 == 0 {
			return Inspection{}, domain.FieldError("file", "stop.sh 必须具有可执行权限")
		}
		result.HasStop = true
	}
	startContent := string(scriptContents[scriptPath("start.sh")])
	actualConfig := RelativeConfigPath(result)
	for _, candidate := range configCandidates {
		if candidate.path != actualConfig && strings.Contains(startContent, candidate.path) {
			return Inspection{}, domain.FieldError(
				"file",
				fmt.Sprintf("start.sh 使用 %s，但安装包提供的是 %s", candidate.path, actualConfig),
			)
		}
	}
	return result, nil
}

func matchConfigPath(name string) (rootPrefix, configType string, ok bool) {
	for _, candidate := range configCandidates {
		if name == candidate.path {
			return "", candidate.configType, true
		}
		suffix := "/" + candidate.path
		if strings.HasSuffix(name, suffix) {
			prefix := strings.TrimSuffix(name, suffix)
			if prefix != "" && !strings.Contains(prefix, "/") {
				return prefix, candidate.configType, true
			}
		}
	}
	return "", "", false
}

func matchRootFile(name, filename string) (rootPrefix, matchedPath string, ok bool) {
	if name == filename {
		return "", name, true
	}
	suffix := "/" + filename
	if strings.HasSuffix(name, suffix) {
		prefix := strings.TrimSuffix(name, suffix)
		if prefix != "" && !strings.Contains(prefix, "/") {
			return prefix, name, true
		}
	}
	return "", "", false
}

func configPort(content []byte, configType string) (int, error) {
	var root map[string]any
	switch configType {
	case "json":
		if err := json.Unmarshal(content, &root); err != nil {
			var syntax *json.SyntaxError
			if errors.As(err, &syntax) {
				return 0, domain.FieldError("content",
					fmt.Sprintf("JSON 配置格式错误，位置 %d", syntax.Offset))
			}
			return 0, domain.FieldError("content", "JSON 配置必须是对象")
		}
	case "yaml":
		decoder := yaml.NewDecoder(strings.NewReader(string(content)))
		if err := decoder.Decode(&root); err != nil {
			return 0, domain.FieldError("content", "YAML 配置格式错误: "+err.Error())
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return 0, domain.FieldError("content", "YAML 配置只能包含一个文档")
		}
	default:
		return 0, errors.New("unsupported config type")
	}
	portValue := root["port"]
	if portValue == nil {
		if server, ok := root["server"].(map[string]any); ok {
			portValue = server["port"]
		}
	}
	port, err := domain.ParsePort(portValue)
	if err != nil {
		return 0, domain.FieldError("content", "配置文件的 port 或 server.port 无效")
	}
	return port, nil
}

func cleanArchivePath(name string) string {
	if name == "" || strings.ContainsAny(name, "\x00\\") || path.IsAbs(name) {
		return ""
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}
