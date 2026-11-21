//go:build ignore

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type pluginManifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	Runtime     string   `json:"runtime"`
	Main        string   `json:"main"`
}

type catalogListing struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	Download    string   `json:"download"`
	SHA256      string   `json:"sha256"`
}

type catalogIndex struct {
	Version int              `json:"version"`
	Plugins []catalogListing `json:"plugins"`
}

func main() {
	outDir := flag.String("out", "dist", "output directory for zips, index.json, and releases.tsv")
	baseURL := flag.String("base-url", "https://github.com/Ciao1019/arcmd-plugins/releases/download", "release download base URL")
	flag.Parse()

	if err := run(*outDir, strings.TrimRight(*baseURL, "/")); err != nil {
		fmt.Fprintf(os.Stderr, "build_catalog: %v\n", err)
		os.Exit(1)
	}
}

func run(outDir, baseURL string) error {
	entries, err := os.ReadDir("plugins")
	if err != nil {
		return fmt.Errorf("read plugins dir: %w", err)
	}
	var pluginDirs []string
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join("plugins", ent.Name())
		if _, err := os.Stat(filepath.Join(dir, "arcmd-plugin.json")); err == nil {
			pluginDirs = append(pluginDirs, dir)
		}
	}
	sort.Strings(pluginDirs)
	if len(pluginDirs) == 0 {
		return fmt.Errorf("no plugins found under plugins/")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	idx := catalogIndex{Version: 1}
	var tsv strings.Builder

	for _, dir := range pluginDirs {
		manBytes, err := os.ReadFile(filepath.Join(dir, "arcmd-plugin.json"))
		if err != nil {
			return err
		}
		var man pluginManifest
		if err := json.Unmarshal(manBytes, &man); err != nil {
			return fmt.Errorf("%s manifest: %w", dir, err)
		}
		if man.ID == "" || man.Name == "" || man.Version == "" || man.Main == "" {
			return fmt.Errorf("%s manifest missing required fields (id/name/version/main)", dir)
		}
		if man.Runtime == "" {
			man.Runtime = "wasm"
		}
		if man.Runtime != "wasm" {
			return fmt.Errorf("%s unsupported runtime %q", dir, man.Runtime)
		}

		tmpDir, err := os.MkdirTemp("", "arcmd-plugin-build-*")
		if err != nil {
			return err
		}
		wasmOut := filepath.Join(tmpDir, "plugin.wasm")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", wasmOut, ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		buildErr := cmd.Run()
		if buildErr != nil {
			_ = os.RemoveAll(tmpDir)
			return fmt.Errorf("build %s: %w", dir, buildErr)
		}
		wasmBytes, readErr := os.ReadFile(wasmOut)
		_ = os.RemoveAll(tmpDir)
		if readErr != nil {
			return readErr
		}

		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		add := func(name string, data []byte) error {
			hdr := &zip.FileHeader{
				Name:     name,
				Method:   zip.Deflate,
				Modified: time.Unix(0, 0).UTC(),
			}
			hdr.SetMode(0o644)
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			_, err = w.Write(data)
			return err
		}
		if err := add("arcmd-plugin.json", manBytes); err != nil {
			return err
		}
		if err := add(man.Main, wasmBytes); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}

		zipName := fmt.Sprintf("%s-%s.zip", man.ID, man.Version)
		zipPath := filepath.Join(outDir, zipName)
		if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
			return err
		}

		sum := sha256.Sum256(buf.Bytes())
		shaHex := hex.EncodeToString(sum[:])
		tag := fmt.Sprintf("%s-v%s", man.ID, man.Version)

		idx.Plugins = append(idx.Plugins, catalogListing{
			ID:          man.ID,
			Name:        man.Name,
			Version:     man.Version,
			Description: man.Description,
			Author:      man.Author,
			Homepage:    man.Homepage,
			Icon:        man.Icon,
			Tags:        man.Tags,
			Permissions: man.Permissions,
			Download:    fmt.Sprintf("%s/%s/%s", baseURL, tag, zipName),
			SHA256:      shaHex,
		})
		fmt.Fprintf(&tsv, "%s\t%s\t%s\t%s\t%s\n", man.ID, man.Version, tag, zipName, shaHex)
		fmt.Printf("built %s (%d bytes wasm, %d bytes zip, sha256=%s)\n", zipPath, len(wasmBytes), buf.Len(), shaHex)
	}

	indexJSON, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	indexJSON = append(indexJSON, '\n')
	if err := os.WriteFile(filepath.Join(outDir, "index.json"), indexJSON, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "releases.tsv"), []byte(tsv.String()), 0o644); err != nil {
		return err
	}
	return nil
}
