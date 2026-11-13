package topology

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const bicepVersion = "0.49.2"

type bicepRelease struct {
	name   string
	size   int64
	sha256 string
}

// Digests and sizes are pinned to the official Azure/bicep v0.49.2 release assets.
var bicepReleases = map[string]bicepRelease{
	"darwin/arm64":  {"bicep-osx-arm64", 118862032, "8ae74a6cf9cbba0cb220c40cf0e955f98d59df950afab07c21b7bcb78c9c5d68"},
	"darwin/amd64":  {"bicep-osx-x64", 124315472, "1403cd4b17178c40ee87a40222ce318a31a2b95db7bbcd964c03780cad89dc5e"},
	"linux/arm64":   {"bicep-linux-arm64", 118882803, "48d8f152bd78c8848a41a07ac2d12b573306d9329fc961a85354a98a95cc275e"},
	"linux/amd64":   {"bicep-linux-x64", 124059770, "0ab9de90c2493f206f8281bae74f9ee2d5ed3605548dab1adf41fef185e1f7c5"},
	"windows/arm64": {"bicep-win-arm64.exe", 124257720, "0576e45158736546ef16bf0f5f6403cb3ec88cc560b23f802bb9d48e0edf65ea"},
	"windows/amd64": {"bicep-win-x64.exe", 126668256, "acfff0453c9bcf442aa97103acbcd845ef366634e6ea2a2ba5734514409ce9c3"},
}

func managedBicepCompiler(ctx context.Context) (string, error) {
	release, exists := bicepReleases[runtime.GOOS+"/"+runtime.GOARCH]
	if !exists {
		return "", fmt.Errorf("managed Bicep compiler is unavailable for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate private Bicep compiler cache: %w", err)
	}
	client := &http.Client{
		Timeout: 3 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" || request.URL.User != nil {
				return fmt.Errorf("Bicep download redirect must use HTTPS without credentials")
			}
			if len(via) >= 10 {
				return fmt.Errorf("too many Bicep download redirects")
			}
			return nil
		},
	}
	directory := filepath.Join(cache, "gh-cao", "tools", "bicep", bicepVersion)
	url := "https://github.com/Azure/bicep/releases/download/v" + bicepVersion + "/" + release.name
	return cacheBicepCompiler(ctx, directory, release, url, client)
}

func verifyBicepCompiler(ctx context.Context, filename string, release bicepRelease) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != release.size {
		return fmt.Errorf("invalid cached Bicep compiler at %s: expected a regular file of %d bytes", filename, release.size)
	}
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(file, release.size+1)); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != release.sha256 {
		return fmt.Errorf("cached Bicep compiler checksum mismatch at %s; remove this file and retry", filename)
	}
	return ctx.Err()
}

func cacheBicepCompiler(ctx context.Context, directory string, release bicepRelease, url string, client *http.Client) (filename string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	filename = filepath.Join(directory, release.name)
	if err := verifyBicepCompiler(ctx, filename, release); err == nil {
		return filename, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create private Bicep compiler cache: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download Bicep %s: %w", bicepVersion, err)
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download Bicep %s: HTTP %d", bicepVersion, response.StatusCode)
	}
	file, err := os.CreateTemp(directory, ".bicep-download-")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	defer func() {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		if temporary != "" {
			err = errors.Join(err, os.Remove(temporary))
		}
	}()
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, release.size+1))
	if err != nil {
		return "", fmt.Errorf("download Bicep %s: %w", bicepVersion, err)
	}
	if size != release.size || hex.EncodeToString(digest.Sum(nil)) != release.sha256 {
		return "", fmt.Errorf("downloaded Bicep %s failed size or SHA-256 verification", bicepVersion)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := file.Chmod(0o700); err != nil {
		return "", err
	}
	closeErr := file.Close()
	file = nil
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(temporary, filename); err != nil {
		// Another process may have published the same pinned compiler first.
		verifyErr := verifyBicepCompiler(ctx, filename, release)
		if verifyErr == nil {
			return filename, nil
		}
		return "", fmt.Errorf("publish verified Bicep compiler: %w", errors.Join(err, verifyErr))
	}
	temporary = ""
	return filename, nil
}
