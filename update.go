package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// version is set at release build time: -ldflags "-X main.version=v1.2.3". Dev builds never self-update.
var version = "dev"

const (
	releaseAPI   = "https://api.github.com/repos/LuGG000/deadlock-counter-build/releases/latest"
	releaseAsset = "deadlock-counter.exe"
	maxExeSize   = 100 << 20
)

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"` // "sha256:<hex>", may be empty
	} `json:"assets"`
}

// parseVersion turns "v1.2.3" into [1 2 3]; ok is false for anything else (e.g. "dev").
func parseVersion(s string) (v [3]int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func isNewer(latest, current string) bool {
	l, ok1 := parseVersion(latest)
	c, ok2 := parseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// selfUpdate replaces the running exe with the latest GitHub release and restarts it.
// It returns only if no update was applied; all failures are logged and non-fatal.
func selfUpdate() {
	if _, ok := parseVersion(version); !ok {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(exe + ".old")

	rel, err := latestRelease()
	if err != nil {
		log.Printf("Update check failed: %v", err)
		return
	}
	if !isNewer(rel.TagName, version) {
		return
	}
	log.Printf("Updating %s -> %s ...", version, rel.TagName)
	if err := installRelease(rel, exe); err != nil {
		log.Printf("Update failed: %v", err)
		return
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("Restart failed, please start the app again: %v", err)
		os.Exit(0)
	}
	os.Exit(0)
}

func latestRelease() (*ghRelease, error) {
	c := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, releaseAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release API returned %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func installRelease(rel *ghRelease, exe string) error {
	var url, digest string
	for _, a := range rel.Assets {
		if a.Name == releaseAsset {
			url, digest = a.URL, strings.TrimPrefix(a.Digest, "sha256:")
		}
	}
	if url == "" {
		return fmt.Errorf("release %s has no asset %s", rel.TagName, releaseAsset)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	tmp := exe + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxExeSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxExeSize {
		err = fmt.Errorf("download exceeds %d bytes", maxExeSize)
	}
	if err == nil && digest != "" && hex.EncodeToString(h.Sum(nil)) != digest {
		err = fmt.Errorf("checksum mismatch")
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}

	// A running exe can be renamed but not overwritten on Windows.
	if err := os.Rename(exe, exe+".old"); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(exe+".old", exe)
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
