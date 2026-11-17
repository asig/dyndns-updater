/*
 * This file is part of dyndns-updater.
 *
 * Copyright (C) 2026 Andreas Signer <asigner@gmail.com>
 *
 * dyndns-updater is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * dyndns-updater is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with dyndns-updater.  If not, see <https://www.gnu.org/licenses/>.
 */

// Package state stores the last successful update.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record identifies a successful update.
type Record struct {
	Host string `json:"host"`
	URL  string `json:"url"`
	IP   string `json:"ip"`
}

// Pause records why updates are suspended. A zero Until requires intervention.
type Pause struct {
	Reason string    `json:"reason"`
	Until  time.Time `json:"until"`
}

// LoadPause reads the persistent update suspension, if present.
func LoadPause(path string) (Pause, error) {
	b, err := os.ReadFile(path + ".blocked")
	if errors.Is(err, os.ErrNotExist) {
		return Pause{}, nil
	}
	if err != nil {
		return Pause{}, fmt.Errorf("read update suspension: %w", err)
	}
	var pause Pause
	if err := json.Unmarshal(b, &pause); err != nil {
		return Pause{}, fmt.Errorf("parse update suspension: %w", err)
	}
	if pause.Reason == "" {
		return Pause{}, fmt.Errorf("parse update suspension: missing reason")
	}
	return pause, nil
}

// SavePause persists an update suspension.
func SavePause(path string, pause Pause) error {
	b, err := json.Marshal(pause)
	if err != nil {
		return fmt.Errorf("encode update suspension: %w", err)
	}
	return writeAtomic(path+".blocked", append(b, '\n'))
}

// ClearPause removes an update suspension.
func ClearPause(path string) error {
	if err := os.Remove(path + ".blocked"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove update suspension: %w", err)
	}
	return nil
}

// ResolvePath resolves directory and file symlinks, including dangling file symlinks.
func ResolvePath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve state path: %w", err)
	}
	for i := 0; i < 255; i++ {
		dir, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return "", fmt.Errorf("resolve state directory: %w", err)
		}
		path = filepath.Join(dir, filepath.Base(path))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect state file %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("read state symlink %q: %w", path, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		path = target
	}
	return "", fmt.Errorf("too many state symlinks")
}

// Load reads the stored update. A missing file returns an empty record.
func Load(path string) (Record, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, nil
		}
		return Record{}, fmt.Errorf("read state file %q: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Record{}, fmt.Errorf("stat state file %q: %w", path, err)
	}
	if info.Mode().IsRegular() && info.Mode().Perm()&0o077 != 0 {
		if err := f.Chmod(info.Mode().Perm() & 0o600); err != nil {
			return Record{}, fmt.Errorf("secure state file %q: %w", path, err)
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return Record{}, fmt.Errorf("read state file %q: %w", path, err)
	}
	// Legacy files contain only the IP, so they cannot identify the target.
	legacyIP := strings.TrimSpace(string(b))
	if ip := net.ParseIP(legacyIP); ip != nil && ip.To4() != nil {
		return Record{IP: legacyIP}, nil
	}
	var record Record
	if err := json.Unmarshal(b, &record); err != nil {
		return Record{}, fmt.Errorf("parse state file %q: %w", path, err)
	}
	if record.Host == "" || record.URL == "" || record.IP == "" {
		return Record{}, fmt.Errorf("parse state file %q: missing host, URL, or IP", path)
	}
	return record, nil
}

// Save writes the successful update to the state file.
func Save(path string, record Record) (err error) {
	b, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	return writeAtomic(path, append(b, '\n'))
}

func writeAtomic(path string, b []byte) (err error) {
	path, err = ResolvePath(path)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".dyndns-state-*")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	tempPath := f.Name()
	defer func() {
		if tempPath != "" {
			if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove temporary state file %q: %w", tempPath, removeErr))
			}
		}
	}()

	_, writeErr := f.Write(b)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return fmt.Errorf("write state file %q: %w", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace state file %q: %w", path, err)
	}
	tempPath = ""
	return nil
}
