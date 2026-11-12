package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const maxCLIBytes = 64 << 20

type cliAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type cliRelease struct {
	Tag        string     `json:"tag_name"`
	Draft      bool       `json:"draft"`
	Prerelease bool       `json:"prerelease"`
	Assets     []cliAsset `json:"assets"`
	Source     string     `json:"source"`
}

type cliUpdateCheck struct {
	RunningVersion   string `json:"running_version"`
	InstalledVersion string `json:"installed_version"`
	LatestVersion    string `json:"latest_version"`
	UpdateAvailable  bool   `json:"update_available"`
	RestartRequired  bool   `json:"restart_required"`
	ReleaseURL       string `json:"release_url"`
	Message          string `json:"message"`
}

type cliUpdater struct {
	client        *http.Client
	executable    func() (string, error)
	binaryVersion func(context.Context, string) (string, error)
	running       string
	goos, goarch  string
	cachePath     string
}

func newCLIUpdater() *cliUpdater {
	cache, _ := os.UserCacheDir()
	if cache != "" {
		cache = filepath.Join(cache, "moo-cloud", "cli-update.json")
	}
	return &cliUpdater{
		client: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 || req.URL.Scheme != "https" {
				return fmt.Errorf("更新下载只允许 HTTPS，且最多重定向 10 次")
			}
			return nil
		}},
		executable: os.Executable, binaryVersion: cliBinaryVersion,
		running: version, goos: runtime.GOOS, goarch: runtime.GOARCH, cachePath: cache,
	}
}

func cliBinaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("读取 CLI 版本失败：%w", err)
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "moo-cloud ") {
		return "", fmt.Errorf("文件不是 moo-cloud CLI")
	}
	return strings.TrimPrefix(s, "moo-cloud "), nil
}

// 只比较稳定发布版本；开发构建不能被自动降回最近一个稳定版本。
func cliSemver(s string) ([3]int, bool) {
	var result [3]int
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != 3 {
		return result, false
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return result, false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return result, false
			}
		}
		n, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return result, false
		}
		result[i] = int(n)
	}
	return result, true
}

func cliNewer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func (u *cliUpdater) assetName() (string, error) {
	if (u.goos != "darwin" && u.goos != "linux" && u.goos != "windows") || (u.goarch != "amd64" && u.goarch != "arm64") {
		return "", fmt.Errorf("暂不提供 %s/%s 的预编译文件", u.goos, u.goarch)
	}
	name := "moo-cloud-" + u.goos + "-" + u.goarch
	if u.goos == "windows" {
		name += ".exe"
	}
	return name, nil
}

func (r cliRelease) baseURL() string {
	if r.Source != "github" && r.Source != "gitee" {
		return ""
	}
	return "https://" + r.Source + ".com/charsen/moo-cloud-cli/releases"
}

func (r cliRelease) asset(name string) (cliAsset, error) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			// 下载入口固定到本项目的 Release，不能用工具参数指定任意可执行文件。
			if r.baseURL() == "" || asset.URL != r.baseURL()+"/download/"+r.Tag+"/"+name {
				return cliAsset{}, fmt.Errorf("发布附件下载地址不属于官方仓库")
			}
			return asset, nil
		}
	}
	return cliAsset{}, fmt.Errorf("发布缺少附件 %s", name)
}

func (u *cliUpdater) validateRelease(r cliRelease) error {
	if _, ok := cliSemver(r.Tag); !ok || r.Draft || r.Prerelease {
		return fmt.Errorf("未找到有效的稳定版本")
	}
	name, err := u.assetName()
	if err != nil {
		return err
	}
	asset, err := r.asset(name)
	if err != nil {
		return err
	}
	if asset.Size <= 0 || asset.Size > maxCLIBytes {
		return fmt.Errorf("发布附件大小不合法")
	}
	_, err = r.asset("SHA256SUMS")
	return err
}

func (u *cliUpdater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// 公开发布检查不读取、发送账号级 MCP token。
	req.Header.Set("User-Agent", "moo-cloud-cli/"+u.running)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问发布服务失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("发布服务返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("发布响应超过大小限制")
	}
	return data, nil
}

func (u *cliUpdater) releaseFrom(ctx context.Context, source string) (cliRelease, error) {
	endpoint := "https://api.github.com/repos/charsen/moo-cloud-cli/releases/latest"
	if source == "gitee" {
		endpoint = "https://gitee.com/api/v5/repos/charsen/moo-cloud-cli/releases/latest"
	} else if source != "github" {
		return cliRelease{}, fmt.Errorf("未知的发布源")
	}
	data, err := u.get(ctx, endpoint, 2<<20)
	var release cliRelease
	if err == nil {
		err = json.Unmarshal(data, &release)
	}
	release.Source = source
	if err == nil {
		err = u.validateRelease(release)
	}
	return release, err
}

func (u *cliUpdater) release(ctx context.Context, cached bool) (cliRelease, error) {
	var cache struct {
		CheckedAt time.Time  `json:"checked_at"`
		Release   cliRelease `json:"release"`
	}
	if cached && u.cachePath != "" {
		data, err := os.ReadFile(u.cachePath)
		if err == nil && json.Unmarshal(data, &cache) == nil && time.Since(cache.CheckedAt) >= 0 && time.Since(cache.CheckedAt) < 24*time.Hour && u.validateRelease(cache.Release) == nil {
			return cache.Release, nil
		}
	}
	var failures []string
	sources := []string{"github", "gitee"}
	for i, source := range sources {
		// 为备用源保留时间，避免 GitHub 连接超时耗尽整个检查预算。
		budget := 8 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline) / time.Duration(len(sources)-i); remaining < budget {
				budget = remaining
			}
		}
		requestCtx, cancel := context.WithTimeout(ctx, budget)
		release, err := u.releaseFrom(requestCtx, source)
		cancel()
		if err != nil {
			failures = append(failures, source+": "+err.Error())
			continue
		}
		cache.CheckedAt, cache.Release = time.Now().UTC(), release
		if u.cachePath != "" {
			// 缓存仅用于减少自动检查的网络请求；写入失败不影响手动检查或更新。
			if err := os.MkdirAll(filepath.Dir(u.cachePath), 0700); err == nil {
				if file, err := os.CreateTemp(filepath.Dir(u.cachePath), ".cli-update-*"); err == nil {
					encodeErr := json.NewEncoder(file).Encode(cache)
					closeErr := file.Close()
					if encodeErr == nil && closeErr == nil {
						_ = os.Rename(file.Name(), u.cachePath)
					}
					_ = os.Remove(file.Name())
				}
			}
		}
		return release, nil
	}
	return cliRelease{}, fmt.Errorf("检查 CLI 更新失败：%s", strings.Join(failures, "; "))
}

func (u *cliUpdater) installed(ctx context.Context) (string, string, error) {
	path, err := u.executable()
	if err != nil {
		return "", "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", err
	}
	v, err := u.binaryVersion(ctx, path)
	return path, v, err
}

func cliFileChanged(original os.FileInfo, path string) bool {
	current, err := os.Stat(path)
	return err != nil || !os.SameFile(original, current) || original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime())
}

func (u *cliUpdater) check(ctx context.Context, cached bool) (cliUpdateCheck, error) {
	_, installed, err := u.installed(ctx)
	if err != nil {
		return cliUpdateCheck{}, err
	}
	release, err := u.release(ctx, cached)
	if err != nil {
		return cliUpdateCheck{}, err
	}
	latest, _ := cliSemver(release.Tag)
	current, stable := cliSemver(installed)
	running, runningStable := cliSemver(u.running)
	result := cliUpdateCheck{
		RunningVersion: u.running, InstalledVersion: installed, LatestVersion: strings.TrimPrefix(release.Tag, "v"),
		UpdateAvailable: stable && cliNewer(latest, current), RestartRequired: runningStable && stable && running != current,
		ReleaseURL: release.baseURL() + "/tag/" + release.Tag,
		Message:    "本机已安装最新稳定版。",
	}
	if !stable {
		result.Message = "本机是开发构建，仅检查发布版本，不自动覆盖开发构建。"
	} else if result.UpdateAvailable {
		result.Message = "发现新版 " + result.LatestVersion + "；先告知用户，明确同意后才可调用 update_cli。"
	} else if result.RestartRequired {
		result.Message = "本机文件已更新；当前 MCP 仍是旧进程，请重新连接 MCP。"
	} else if cliNewer(current, latest) {
		result.Message = "本机版本高于当前发布源，无需更新。"
	}
	return result, nil
}

func cliChecksum(data []byte, name string) (string, error) {
	checksum := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size || checksum != "" {
			return "", fmt.Errorf("SHA256SUMS 中校验和非法或重复")
		}
		checksum = strings.ToLower(fields[0])
	}
	if checksum == "" {
		return "", fmt.Errorf("SHA256SUMS 缺少 %s", name)
	}
	return checksum, nil
}

func (u *cliUpdater) download(ctx context.Context, release cliRelease) ([]byte, error) {
	name, _ := u.assetName()
	asset, _ := release.asset(name)
	sums, _ := release.asset("SHA256SUMS")
	data, err := u.get(ctx, sums.URL, 64<<10)
	if err != nil {
		return nil, err
	}
	expected, err := cliChecksum(data, name)
	if err != nil {
		return nil, err
	}
	data, err = u.get(ctx, asset.URL, maxCLIBytes)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != asset.Size || hex.EncodeToString(digest[:]) != expected {
		return nil, fmt.Errorf("下载文件大小或 SHA-256 不匹配，保留原文件")
	}
	return data, nil
}

func (u *cliUpdater) update(ctx context.Context, target string, confirmed bool) (map[string]any, error) {
	// 版本号和确认必须同时提供，避免检查后出现另一个版本时扩大用户的授权。
	targetVersion, valid := cliSemver(target)
	if !confirmed || !valid {
		return nil, fmt.Errorf("必须先告知用户目标版本并取得明确同意，再传 target_version 和 confirm=true")
	}
	path, installed, err := u.installed(ctx)
	if err != nil {
		return nil, err
	}
	current, stable := cliSemver(installed)
	if !stable {
		return nil, fmt.Errorf("开发构建不能通过自更新覆盖，请手动安装正式版")
	}
	if current == targetVersion {
		return map[string]any{"updated": false, "installed_version": installed, "restart_required": u.running != installed, "message": "目标版本已安装；旧 MCP 进程需重新连接。"}, nil
	}
	if !cliNewer(targetVersion, current) {
		return nil, fmt.Errorf("目标版本必须高于本机版本，不允许降级")
	}
	unlock, err := acquireCLIUpdateLock(path + ".update.lock")
	if err != nil {
		return nil, fmt.Errorf("无法取得更新锁（可能正在更新或目录不可写）：%w", err)
	}
	defer unlock()
	originalFile, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	// 三个客户端可能共用同一文件；锁外读取的版本不能作为最终写入依据。
	lockedVersion, err := u.binaryVersion(ctx, path)
	if err != nil || lockedVersion != installed {
		return nil, fmt.Errorf("本机文件已变化，请重新检查版本后再更新")
	}
	// 不信任检查缓存：重新获取发布，并核对仍是用户确认的版本。
	release, err := u.release(ctx, false)
	if err != nil {
		return nil, err
	}
	latest, _ := cliSemver(release.Tag)
	if latest != targetVersion {
		return nil, fmt.Errorf("最新发布已变为 %s，请重新告知用户并确认", release.Tag)
	}
	// API 可达不代表附件 CDN 可达；为另一个源的同版本附件保留下载时间。
	budget := 40 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline)/2 < budget {
		budget = time.Until(deadline) / 2
	}
	downloadCtx, cancel := context.WithTimeout(ctx, budget)
	data, err := u.download(downloadCtx, release)
	cancel()
	if err != nil {
		primaryErr := err
		fallback := "gitee"
		if release.Source == "gitee" {
			fallback = "github"
		}
		release, err = u.releaseFrom(ctx, fallback)
		if err == nil {
			if latest, _ := cliSemver(release.Tag); latest != targetVersion {
				err = fmt.Errorf("备用源发布为 %s，与确认版本不同", release.Tag)
			} else {
				data, err = u.download(ctx, release)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("附件下载失败：主源 %v；备用源 %v", primaryErr, err)
		}
	}
	// 暂存文件必须与目标同目录，Unix 才能通过 rename 原子替换正在使用的文件。
	pattern := ".moo-cloud-update-*"
	if u.goos == "windows" {
		pattern += ".exe"
	}
	candidate, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return nil, err
	}
	defer os.Remove(candidate.Name())
	_, writeErr := candidate.Write(data)
	syncErr := candidate.Sync()
	closeErr := candidate.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return nil, fmt.Errorf("写入更新文件失败：write=%v sync=%v close=%v", writeErr, syncErr, closeErr)
	}
	if err := os.Chmod(candidate.Name(), 0755); err != nil {
		return nil, err
	}
	actual, err := u.binaryVersion(ctx, candidate.Name())
	if parsed, ok := cliSemver(actual); err != nil || !ok || parsed != targetVersion {
		return nil, fmt.Errorf("下载文件无法运行或版本不匹配，保留原文件")
	}
	// 手动安装不遵守本工具的锁；下载后仍须核对文件身份，避免覆盖期间装上的其他版本。
	if cliFileChanged(originalFile, path) {
		return nil, fmt.Errorf("下载期间本机文件已变化，请重新检查版本")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backup, err := os.CreateTemp(filepath.Dir(path), ".moo-cloud-backup-*")
	if err != nil {
		return nil, err
	}
	backupPath := backup.Name()
	keepBackup := false
	defer func() {
		if !keepBackup {
			_ = os.Remove(backupPath)
		}
	}()
	old, err := os.Open(path)
	if err != nil {
		_ = backup.Close()
		return nil, err
	}
	_, copyErr := io.Copy(backup, old)
	_ = old.Close()
	syncErr = backup.Sync()
	closeErr = backup.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		return nil, fmt.Errorf("备份原文件失败，取消更新")
	}
	if err := os.Chmod(backupPath, 0755); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cliFileChanged(originalFile, path) {
		return nil, fmt.Errorf("备份期间本机文件已变化，请重新检查版本")
	}
	if err := os.Rename(candidate.Name(), path); err != nil {
		// Windows 可能锁住正在运行的 exe；此时不退出宿主、不删除原文件，让用户手动替换。
		return nil, fmt.Errorf("替换失败，原文件保持不变；请关闭 MCP 后手动更新：%w", err)
	}
	keepBackup = true
	return map[string]any{"updated": true, "installed_version": actual, "previous_version": installed, "backup_path": backupPath, "restart_required": true, "message": "CLI 文件已更新。请重新连接 MCP 或重启客户端；当前连接继续使用旧进程。"}, nil
}
