//go:build linux

package install_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstall(t *testing.T) {
	const app = "lazyp4"
	const amd64Asset = "lazyp4_linux_x86_64.tar.gz"
	const arm64Asset = "lazyp4_linux_arm64.tar.gz"
	for _, tool := range []string{"tar", "sha256sum", "awk", "mktemp", "install", "mv"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is unavailable", tool)
		}
	}
	for _, tc := range []struct {
		name, machine, asset, system, wantError                                                   string
		badChecksum, missingChecksum, badArchive, downloadFailure, installFailure, missingRelease bool
	}{
		{name: "x86_64", machine: "x86_64", asset: amd64Asset},
		{name: "aarch64", machine: "aarch64", asset: arm64Asset},
		{name: "arm64", machine: "arm64", asset: arm64Asset},
		{name: "unsupported architecture", machine: "i686", wantError: "Supported architectures:"},
		{name: "unsupported OS", machine: "x86_64", system: "Darwin", wantError: "Linux only"},
		{name: "missing release", machine: "x86_64", missingRelease: true, wantError: "No stable release found"},
		{name: "bad checksum", machine: "x86_64", badChecksum: true, wantError: "Checksum verification failed"},
		{name: "missing checksum", machine: "x86_64", missingChecksum: true, wantError: "Checksum verification failed"},
		{name: "bad archive", machine: "x86_64", badArchive: true},
		{name: "download failure", machine: "x86_64", downloadFailure: true},
		{name: "install failure", machine: "x86_64", installFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tools := filepath.Join(root, "tools")
			fixtures := filepath.Join(root, "fixtures")
			temp := filepath.Join(root, "temporary files")
			dest := filepath.Join(root, "bin with spaces")
			for _, dir := range []string{tools, fixtures, temp, dest} {
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path string, data []byte, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, data, mode); err != nil {
					t.Fatal(err)
				}
			}
			payload := []byte("new version")
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: app, Mode: 0755, Size: int64(len(payload))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			data := archive.Bytes()
			if tc.badArchive {
				data = []byte("invalid archive")
			}
			asset := tc.asset
			if asset == "" {
				asset = amd64Asset
			}
			checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(data), asset)
			if tc.badChecksum {
				checksum = strings.Repeat("0", 64) + "  " + asset + "\n"
			}
			if tc.missingChecksum {
				checksum = strings.Repeat("0", 64) + "  other.tar.gz\n"
			}
			write(filepath.Join(fixtures, asset), data, 0600)
			write(filepath.Join(fixtures, "checksums.txt"), []byte(checksum), 0600)
			previous := []byte("previous version")
			installed := filepath.Join(dest, app)
			write(installed, previous, 0755)
			write(filepath.Join(tools, "uname"), []byte(`#!/bin/sh
case "$1" in
    -s) printf '%s\n' "$INSTALL_TEST_SYSTEM" ;;
    -m) printf '%s\n' "$INSTALL_TEST_MACHINE" ;;
    *) exit 1 ;;
esac
`), 0755)
			write(filepath.Join(tools, "curl"), []byte(`#!/bin/sh
set -eu
output=''
url=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output=$2; shift 2 ;;
        -w) shift 2 ;;
        -*) shift ;;
        *) url=$1; shift ;;
    esac
done
base=https://github.com/Solessfir/$INSTALL_TEST_APP
case "$url" in
    "$base/releases/latest")
        if [ "$INSTALL_TEST_RELEASE_MISSING" = true ]; then
            printf '%s\n' "$base/releases"
        else
            printf '%s\n' "$base/releases/tag/v1.2.3"
        fi ;;
    "$base/releases/download/v1.2.3/"*)
        [ "$INSTALL_TEST_DOWNLOAD_FAILURE" = false ] || exit 22
        cp -- "$INSTALL_TEST_FIXTURES/${url##*/}" "$output" ;;
    *) exit 22 ;;
esac
`), 0755)
			if tc.installFailure {
				write(filepath.Join(tools, "install"), []byte("#!/bin/sh\nexit 1\n"), 0755)
			}
			system := tc.system
			if system == "" {
				system = "Linux"
			}
			cmd := exec.Command("sh", "install.sh", dest)
			cmd.Env = append(os.Environ(),
				"PATH="+tools+":"+os.Getenv("PATH"), "TMPDIR="+temp,
				"INSTALL_TEST_APP="+app, "INSTALL_TEST_SYSTEM="+system,
				"INSTALL_TEST_MACHINE="+tc.machine, "INSTALL_TEST_FIXTURES="+fixtures,
				fmt.Sprintf("INSTALL_TEST_RELEASE_MISSING=%t", tc.missingRelease),
				fmt.Sprintf("INSTALL_TEST_DOWNLOAD_FAILURE=%t", tc.downloadFailure))
			output, err := cmd.CombinedOutput()
			wantFailure := tc.wantError != "" || tc.badArchive || tc.downloadFailure || tc.installFailure
			if (err != nil) != wantFailure {
				t.Fatalf("installer error = %v, want failure %t: %s", err, wantFailure, output)
			}
			if tc.wantError != "" && !strings.Contains(string(output), tc.wantError) {
				t.Fatalf("missing error %q: %s", tc.wantError, output)
			}
			got, err := os.ReadFile(installed)
			if err != nil {
				t.Fatal(err)
			}
			want := payload
			if wantFailure {
				want = previous
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("installed contents = %q, want %q", got, want)
			}
			info, err := os.Stat(installed)
			if err != nil || info.Mode().Perm() != 0755 {
				t.Fatalf("incorrect installed permissions: %v, %v", info, err)
			}
			for _, dir := range []string{temp, dest} {
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				if (dir == temp && len(entries) != 0) || (dir == dest && (len(entries) != 1 || entries[0].Name() != app)) {
					t.Fatalf("installer left unexpected files in %s: %v", dir, entries)
				}
			}
		})
	}
}
