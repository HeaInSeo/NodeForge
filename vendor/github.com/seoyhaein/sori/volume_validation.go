package sori

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seoyhaein/sori/archiveutil"
)

func loadMetadataJSON(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notFoundError("loadMetadataJSON", fmt.Sprintf("read JSON file %s", path), err)
		}
		return nil, transportError("loadMetadataJSON", fmt.Sprintf("read JSON file %s", path), err)
	}
	var tmp interface{}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return nil, validationError("loadMetadataJSON", fmt.Sprintf("invalid JSON in %s", path), err)
	}
	return data, nil
}

func GenerateVolumeIndex(rootPath, displayName string) (*VolumeIndex, error) {
	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	rootBase := filepath.Base(rootPath)
	var parts []Partition
	err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return transportError("GenerateVolumeIndex", fmt.Sprintf("access %s", path), err)
		}
		if path == rootPath || !d.IsDir() {
			return nil
		}

		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return transportError("GenerateVolumeIndex", fmt.Sprintf("read dir %s", path), readErr)
		}
		hasMarker := false
		for _, e := range entries {
			if e.Name() == "no_deep_scan" && !e.IsDir() {
				hasMarker = true
				break
			}
		}

		rel, relErr := filepath.Rel(rootPath, path)
		if relErr != nil {
			return transportError("GenerateVolumeIndex", fmt.Sprintf("get rel path for %s", path), relErr)
		}
		slashRel := filepath.ToSlash(rel)
		fullPath := fmt.Sprintf("%s/%s", rootBase, slashRel)
		parts = append(parts, Partition{
			Name:        d.Name(),
			Path:        fullPath,
			ManifestRef: "",
			CreatedAt:   now,
			Compression: "",
		})

		if hasMarker {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &VolumeIndex{
		VolumeRef:   "",
		DisplayName: displayName,
		CreatedAt:   now,
		Partitions:  parts,
	}, nil
}

func (vi *VolumeIndex) SaveToFile(rootPath string) error {
	outFile := filepath.Join(rootPath, VolumeIndexJson)
	data, err := json.MarshalIndent(vi, "", "  ")
	if err != nil {
		return transportError("VolumeIndex.SaveToFile", "marshal volume index", err)
	}
	if err := os.WriteFile(outFile, data, 0o644); err != nil {
		return transportError("VolumeIndex.SaveToFile", fmt.Sprintf("write file %s", outFile), err)
	}
	return nil
}

func ValidateVolumeDir(volDir string) ([]byte, error) {
	info, err := os.Stat(volDir)
	if err != nil {
		return nil, notFoundError("ValidateVolumeDir", fmt.Sprintf("volume dir %q does not exist", volDir), err)
	}
	if !info.IsDir() {
		return nil, validationError("ValidateVolumeDir", fmt.Sprintf("volume path %q is not a directory", volDir), nil)
	}

	entries, err := os.ReadDir(volDir)
	if err != nil {
		return nil, transportError("ValidateVolumeDir", fmt.Sprintf("read directory %q", volDir), err)
	}
	visibleCount := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			Log.Warnf("ValidateVolumeDir: hidden entry %q found in %s, skipping", name, volDir)
			continue
		}
		if name == ConfigBlobJson {
			continue
		}
		visibleCount++
	}
	if visibleCount == 0 {
		return nil, validationError("ValidateVolumeDir", fmt.Sprintf("volume directory %q is empty (only hidden files present)", volDir), nil)
	}

	cfgPath := filepath.Join(volDir, ConfigBlobJson)
	raw, err := loadMetadataJSON(cfgPath)
	if err != nil {
		if os.IsNotExist(errors.Unwrap(err)) || strings.Contains(err.Error(), "no such file") {
			Log.Infof("ValidateVolumeDir: %q not found; creating a new empty configblob.json", cfgPath)
			raw = []byte("{}")
			if writeErr := os.WriteFile(cfgPath, raw, fs.FileMode(0644)); writeErr != nil {
				return nil, transportError("ValidateVolumeDir", fmt.Sprintf("create %q", cfgPath), writeErr)
			}
		} else {
			return nil, err
		}
	}
	return raw, nil
}

func writeVolumeIndex(destRoot string, vi *VolumeIndex) error {
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return transportError("writeVolumeIndex", fmt.Sprintf("create destination root %s", destRoot), err)
	}

	indexPath := filepath.Join(destRoot, VolumeIndexJson)
	indexBytes, err := json.MarshalIndent(vi, "", "  ")
	if err != nil {
		return transportError("writeVolumeIndex", "marshal VolumeIndex", err)
	}
	if err := os.WriteFile(indexPath, indexBytes, 0o644); err != nil {
		return transportError("writeVolumeIndex", fmt.Sprintf("write %s", indexPath), err)
	}
	return nil
}

func dirRegularFileSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return transportError("dirRegularFileSize", fmt.Sprintf("walk %s", path), err)
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return transportError("dirRegularFileSize", fmt.Sprintf("stat %s", path), err)
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func validateJSONBytes(data []byte) error {
	var tmp interface{}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return validationError("validateJSONBytes", "invalid JSON payload", err)
	}
	return nil
}

func UntarGzDir(gzipStream io.Reader, dest string) error {
	return archiveutil.UntarGzDir(gzipStream, dest)
}

func TarGzDir(fsDir, prefixPath string) ([]byte, error) {
	return archiveutil.TarGzDir(fsDir, prefixPath)
}
