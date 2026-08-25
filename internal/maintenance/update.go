package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const maxUpdateBytes = 512 << 20

type ReleaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type Release struct {
	Tag        string         `json:"tag_name"`
	URL        string         `json:"html_url"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []ReleaseAsset `json:"assets"`
}

type UpdateOptions struct {
	CurrentVersion string
	Repository     string
	APIURL         string
	Executable     string
	HTTPClient     *http.Client
	GOOS           string
	GOARCH         string
}

type UpdateCheck struct {
	Current    string       `json:"current"`
	Latest     string       `json:"latest"`
	Available  bool         `json:"available"`
	ReleaseURL string       `json:"releaseUrl"`
	Asset      ReleaseAsset `json:"asset"`
}

type Updater struct {
	options UpdateOptions
	http    *http.Client
}

func NewUpdater(options UpdateOptions) (*Updater, error) {
	if options.Repository == "" {
		options.Repository = "Viking602/azem"
	}
	if options.APIURL == "" {
		options.APIURL = "https://api.github.com/repos/" + options.Repository + "/releases/latest"
	}
	parsed, err := url.Parse(options.APIURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) {
		return nil, errors.New("update API URL must use HTTPS outside loopback")
	}
	if options.Executable == "" {
		options.Executable, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.GOARCH == "" {
		options.GOARCH = runtime.GOARCH
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = safeUpdateRedirect
	return &Updater{options: options, http: &copyClient}, nil
}

func (updater *Updater) Check(ctx context.Context) (UpdateCheck, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, updater.options.APIURL, nil)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := updater.http.Do(request)
	if err != nil {
		return UpdateCheck{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return UpdateCheck{}, fmt.Errorf("release API returned HTTP %d", response.StatusCode)
	}
	var release Release
	if json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&release) != nil || release.Draft || release.Tag == "" || !semver.IsValid(normalizeVersion(release.Tag)) {
		return UpdateCheck{}, errors.New("release API returned invalid metadata")
	}
	assetName := "azem-" + updater.options.GOOS + "-" + updater.options.GOARCH
	if updater.options.GOOS == "windows" {
		assetName += ".exe"
	}
	var asset ReleaseAsset
	for _, candidate := range release.Assets {
		if candidate.Name == assetName {
			asset = candidate
			break
		}
	}
	if asset.Name == "" || asset.URL == "" || asset.Size <= 0 || asset.Size > maxUpdateBytes || !validAssetDigest(asset.Digest) {
		return UpdateCheck{}, fmt.Errorf("release %s has no verified %s asset", release.Tag, assetName)
	}
	current, latest := normalizeVersion(updater.options.CurrentVersion), normalizeVersion(release.Tag)
	available := !semver.IsValid(current) || semver.Compare(latest, current) > 0
	return UpdateCheck{Current: updater.options.CurrentVersion, Latest: release.Tag, Available: available, ReleaseURL: release.URL, Asset: asset}, nil
}

func (updater *Updater) Apply(ctx context.Context, check UpdateCheck) error {
	if !check.Available {
		return errors.New("no update is available")
	}
	info, err := os.Lstat(updater.options.Executable)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("current executable must be a regular non-symlink file")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, check.Asset.URL, nil)
	response, err := updater.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("update asset returned HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxUpdateBytes+1))
	if err != nil || len(payload) > maxUpdateBytes || int64(len(payload)) != check.Asset.Size {
		return errors.New("downloaded update size does not match release metadata")
	}
	digest := sha256.Sum256(payload)
	if "sha256:"+hex.EncodeToString(digest[:]) != strings.ToLower(check.Asset.Digest) {
		return errors.New("downloaded update digest does not match release metadata")
	}
	directory := filepath.Dir(updater.options.Executable)
	temporary, err := os.CreateTemp(directory, ".azem-update-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o755); err != nil {
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(verifyCtx, temporaryPath, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("downloaded update failed version verification: %w", err)
	}
	if !strings.Contains(string(output), strings.TrimPrefix(check.Latest, "v")) {
		return errors.New("downloaded update reported the wrong version")
	}
	backup := updater.options.Executable + ".previous"
	_ = os.Remove(backup)
	if err := os.Rename(updater.options.Executable, backup); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, updater.options.Executable); err != nil {
		_ = os.Rename(backup, updater.options.Executable)
		return err
	}
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func safeUpdateRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many update redirects")
	}
	host := strings.ToLower(request.URL.Hostname())
	if request.URL.Scheme != "https" || !(host == "github.com" || host == "api.github.com" || strings.HasSuffix(host, ".githubusercontent.com")) {
		return errors.New("update redirect left trusted GitHub hosts")
	}
	return nil
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "v") {
		value = "v" + value
	}
	return value
}

func validAssetDigest(value string) bool {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
