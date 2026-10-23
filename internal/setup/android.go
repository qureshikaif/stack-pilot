package setup

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Google's SDK repository index lists every SDK package with per-platform
// download URLs and checksums. Android Studio and sdkmanager use the same file.
const androidRepo = "https://dl.google.com/android/repository/"

type sdkArchive struct {
	URL      string
	SHA1     string
	Size     int64
	HostOS   string
	HostArch string
}

type sdkRepository struct {
	Packages []struct {
		Path     string `xml:"path,attr"`
		Archives []struct {
			URL      string `xml:"complete>url"`
			Checksum string `xml:"complete>checksum"`
			Size     int64  `xml:"complete>size"`
			HostOS   string `xml:"host-os"`
			HostArch string `xml:"host-arch"`
		} `xml:"archives>archive"`
	} `xml:"remotePackage"`
}

// pickCmdlineTools finds the cmdline-tools;latest archive for goos/goarch in a
// repository index.
func pickCmdlineTools(r io.Reader, goos, goarch string) (sdkArchive, error) {
	var repo sdkRepository
	if err := xml.NewDecoder(r).Decode(&repo); err != nil {
		return sdkArchive{}, fmt.Errorf("reading Android SDK index: %w", err)
	}
	hostOS := map[string]string{"darwin": "macosx", "linux": "linux", "windows": "windows"}[goos]
	hostArch := map[string]string{"arm64": "aarch64", "amd64": "x64"}[goarch]

	var fallback *sdkArchive
	for _, p := range repo.Packages {
		if p.Path != "cmdline-tools;latest" {
			continue
		}
		for _, a := range p.Archives {
			if a.HostOS != hostOS {
				continue
			}
			arc := sdkArchive{URL: a.URL, SHA1: a.Checksum, Size: a.Size, HostOS: a.HostOS, HostArch: a.HostArch}
			if a.HostArch == hostArch {
				return arc, nil
			}
			if a.HostArch == "" {
				fallback = &arc
			}
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return sdkArchive{}, fmt.Errorf("no Android command-line tools published for %s/%s", goos, goarch)
}

func installCmdlineTools(ctx context.Context, e *Env) error {
	if err := os.MkdirAll(e.sdk("cmdline-tools"), 0o755); err != nil {
		return err
	}

	fmt.Fprintln(e.out, "  fetching Android SDK index…")
	index, err := httpGet(ctx, androidRepo+"repository2-3.xml")
	if err != nil {
		return err
	}
	arc, err := pickCmdlineTools(index, e.OS, e.Arch)
	index.Close()
	if err != nil {
		return err
	}

	zipFile, err := os.CreateTemp(e.AndroidHome, ".stackpilot-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(zipFile.Name())
	defer zipFile.Close()

	if err := download(ctx, e.out, androidRepo+arc.URL, arc.Size, arc.SHA1, zipFile); err != nil {
		return err
	}

	tmp, err := os.MkdirTemp(e.AndroidHome, ".stackpilot-extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	fmt.Fprintln(e.out, "  extracting…")
	if err := extractZip(zipFile.Name(), tmp); err != nil {
		return err
	}

	// The archive contains a top-level cmdline-tools/ directory; the SDK
	// expects it at cmdline-tools/latest.
	src := filepath.Join(tmp, "cmdline-tools")
	dst := e.sdk("cmdline-tools", "latest")
	if exists(dst) {
		backup := dst + ".bak-" + time.Now().Format("20060102-150405")
		fmt.Fprintf(e.out, "  moving existing %s to %s\n", e.tilde(dst), e.tilde(backup))
		if err := os.Rename(dst, backup); err != nil {
			return err
		}
	}
	return os.Rename(src, dst)
}

func httpGet(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

// download streams url into w, printing progress, and verifies its SHA-1.
func download(ctx context.Context, out io.Writer, url string, size int64, wantSHA1 string, w io.Writer) error {
	body, err := httpGet(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()

	h := sha1.New()
	p := &progress{out: out, total: size, label: fmt.Sprintf("  downloading %s (%d MB)", filepath.Base(url), size>>20)}
	n, err := io.Copy(io.MultiWriter(w, h, p), body)
	fmt.Fprintln(out)
	if err != nil {
		return err
	}
	if size > 0 && n != size {
		return fmt.Errorf("download incomplete: got %d of %d bytes", n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantSHA1) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, wantSHA1)
	}
	fmt.Fprintln(out, "  checksum verified")
	return nil
}

type progress struct {
	out         io.Writer
	label       string
	total, done int64
	lastPct     int64
}

func (p *progress) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.total > 0 {
		if pct := p.done * 100 / p.total; pct >= p.lastPct+5 || p.done == p.total {
			p.lastPct = pct
			fmt.Fprintf(p.out, "\r%s %3d%%", p.label, pct)
		}
	}
	return len(b), nil
}

// extractZip unpacks src into dest, refusing entries that escape dest and
// preserving executable bits and symlinks.
func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	root := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range r.File {
		path := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(path, root) {
			return fmt.Errorf("archive entry %q escapes destination", f.Name)
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		case mode&os.ModeSymlink != 0:
			if err := extractSymlink(f, path, root); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := extractFile(f, path, mode.Perm()|0o600); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, path string, perm os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func extractSymlink(f *zip.File, path, root string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	target, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return err
	}
	resolved := filepath.Join(filepath.Dir(path), string(target))
	if filepath.IsAbs(string(target)) || !strings.HasPrefix(resolved, root) {
		return fmt.Errorf("archive symlink %q points outside destination", f.Name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.Symlink(string(target), path)
}
