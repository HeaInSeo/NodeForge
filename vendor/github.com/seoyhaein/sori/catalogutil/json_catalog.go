package catalogutil

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func LoadOrInit[T any](rootDir, fileName string, zero T) (*T, error) {
	path := filepath.Join(rootDir, fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &zero, nil
		}
		return nil, transportError("LoadOrInit", "read catalog "+path, err)
	}

	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, validationError("LoadOrInit", "unmarshal catalog "+path, err)
	}
	return &out, nil
}

func Save(rootDir, fileName string, value any) error {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return transportError("Save", "create catalog dir "+rootDir, err)
	}
	path := filepath.Join(rootDir, fileName)
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return transportError("Save", "marshal catalog "+path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return transportError("Save", "write catalog "+path, err)
	}
	return nil
}
