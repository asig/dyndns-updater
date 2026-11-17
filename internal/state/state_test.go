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

package state

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "last-ip")

	if got, err := Load(p); err != nil || got != (Record{}) {
		t.Fatalf("expected empty state, got %+v, %v", got, err)
	}
	want := Record{Host: "h.example.org", URL: "https://example.org/nic/update", IP: "203.0.113.7"}
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(p); err != nil || got != want {
		t.Fatalf("expected %+v, got %+v, %v", want, got, err)
	}
}

func TestLoadLegacyIP(t *testing.T) {
	p := filepath.Join(t.TempDir(), "last-ip")
	if err := os.WriteFile(p, []byte("203.0.113.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got != (Record{IP: "203.0.113.7"}) {
		t.Fatalf("unexpected legacy state: %+v, %v", got, err)
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err == nil {
		t.Fatal("expected an error reading a directory")
	}
	p := filepath.Join(dir, "invalid")
	if err := os.WriteFile(p, []byte("not a state file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected an error parsing invalid state")
	}
}

func TestSaveReplacesExistingFilePrivately(t *testing.T) {
	p := filepath.Join(t.TempDir(), "last-ip")
	const old = "203.0.113.7\n"
	if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	oldFile, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer oldFile.Close()

	want := Record{Host: "h.example.org", URL: "https://example.org/update?token=secret", IP: "203.0.113.7"}
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	original, err := io.ReadAll(oldFile)
	if err != nil || string(original) != old {
		t.Fatalf("previous file was modified: %q, %v", original, err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected mode 0600, got %04o", got)
	}
	if got, err := Load(p); err != nil || got != want {
		t.Fatalf("expected %+v, got %+v, %v", want, got, err)
	}
}

func TestSaveCleansUpAfterReplaceFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Record{Host: "h", URL: "https://example.org", IP: "203.0.113.7"}); err == nil {
		t.Fatal("expected an error replacing a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state" || !entries[0].IsDir() {
		t.Fatalf("unexpected directory contents: %v", entries)
	}
}

func TestLoadSecuresExistingState(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state")
	want := Record{Host: "h", URL: "https://example.org/update?token=secret", IP: "203.0.113.7"}
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(p); err != nil || got != want {
		t.Fatalf("expected %+v, got %+v, %v", want, got, err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected mode 0600, got %04o", got)
	}
}

func TestSaveFollowsExistingSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("203.0.113.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", link); err != nil {
		t.Fatal(err)
	}
	want := Record{Host: "h", URL: "https://example.org", IP: "203.0.113.8"}
	if err := Save(link, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected state path to remain a symlink, got %v, %v", info, err)
	}
	if got, err := Load(target); err != nil || got != want {
		t.Fatalf("expected %+v, got %+v, %v", want, got, err)
	}
}

func TestSaveFollowsDanglingSymlinkChain(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	second := filepath.Join(dir, "second")
	link := filepath.Join(dir, "link")
	if err := os.Symlink("target", second); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("second", link); err != nil {
		t.Fatal(err)
	}
	want := Record{Host: "h", URL: "https://example.org", IP: "203.0.113.8"}
	if err := Save(link, want); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(target); err != nil || got != want {
		t.Fatalf("expected %+v, got %+v, %v", want, got, err)
	}
	for _, path := range []string{link, second} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected %q to remain a symlink, got %v, %v", path, info, err)
		}
	}
}

func TestResolvePathRejectsSymlinkCycles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loop")
	if err := os.Symlink("loop", path); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolvePath(path); err == nil {
		t.Fatal("expected an error for a symlink cycle")
	}
}
