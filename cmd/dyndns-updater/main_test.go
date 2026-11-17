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

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asig/dyndns-updater/internal/ipcheck"
	"github.com/asig/dyndns-updater/internal/state"
	"github.com/asig/dyndns-updater/internal/updater"
)

func TestRunTracksUpdateTarget(t *testing.T) {
	updates := 0
	ip := "203.0.113.7"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ip" {
			fmt.Fprint(w, ip)
			return
		}
		updates++
		fmt.Fprint(w, "good "+ip)
	}))
	defer srv.Close()

	checker := ipcheck.New()
	checker.Services = []string{srv.URL + "/ip"}
	client := updater.New(srv.URL+"/update", "user", "key")
	path := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(path, []byte(ip+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runAndCheck := func(host string, want int) {
		t.Helper()
		if err := run(ctx, checker, client, host, path); err != nil {
			t.Fatal(err)
		}
		if updates != want {
			t.Fatalf("expected %d updates, got %d", want, updates)
		}
	}
	runAndCheck("first.example.org", 1) // Legacy state must be refreshed.
	runAndCheck("first.example.org", 1)
	runAndCheck("second.example.org", 2)
	client.BaseURL = srv.URL + "/other-update"
	runAndCheck("second.example.org", 3)
	ip = "203.0.113.8"
	runAndCheck("second.example.org", 4)
}

func TestRunStopsOnStateReadError(t *testing.T) {
	updates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ip" {
			fmt.Fprint(w, "203.0.113.7")
			return
		}
		updates++
		fmt.Fprint(w, "good 203.0.113.7")
	}))
	defer srv.Close()
	checker := ipcheck.New()
	checker.Services = []string{srv.URL + "/ip"}
	client := updater.New(srv.URL+"/update", "user", "key")
	if err := run(context.Background(), checker, client, "h.example.org", t.TempDir()); err == nil {
		t.Fatal("expected a state read error")
	}
	if updates != 0 {
		t.Fatalf("unexpected updates: %d", updates)
	}
}

func TestRunDoesNotSaveFailedUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ip" {
			fmt.Fprint(w, "203.0.113.7")
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "good 203.0.113.7")
	}))
	defer srv.Close()
	checker := ipcheck.New()
	checker.Services = []string{srv.URL + "/ip"}
	client := updater.New(srv.URL+"/update", "user", "key")
	path := filepath.Join(t.TempDir(), "state")
	if err := run(context.Background(), checker, client, "h.example.org", path); err == nil {
		t.Fatal("expected an update error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no state file, got %v", err)
	}
}

func TestRunRetriesAfterMismatchedResponseIP(t *testing.T) {
	updates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ip" {
			fmt.Fprint(w, "203.0.113.7")
			return
		}
		updates++
		if updates == 1 {
			fmt.Fprint(w, "good 203.0.113.8")
			return
		}
		fmt.Fprint(w, "good 203.0.113.7")
	}))
	defer srv.Close()
	checker := ipcheck.New()
	checker.Services = []string{srv.URL + "/ip"}
	client := updater.New(srv.URL+"/update", "user", "key")
	path := filepath.Join(t.TempDir(), "state")
	if err := run(context.Background(), checker, client, "h.example.org", path); err == nil {
		t.Fatal("expected an error for a mismatched response IP")
	}
	if err := run(context.Background(), checker, client, "h.example.org", path); err != nil {
		t.Fatal(err)
	}
	if updates != 2 {
		t.Fatalf("expected a retry, got %d update requests", updates)
	}
}

func TestRunWithDanglingStateSymlink(t *testing.T) {
	var updates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ip" {
			fmt.Fprint(w, "203.0.113.7")
			return
		}
		updates.Add(1)
		fmt.Fprint(w, "good 203.0.113.7")
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "state-link")
	if err := os.Symlink("state", path); err != nil {
		t.Fatal(err)
	}
	checker := ipcheck.New()
	checker.Services = []string{srv.URL + "/ip"}
	client := updater.New(srv.URL+"/update", "user", "key")
	for i := 0; i < 2; i++ {
		if err := run(context.Background(), checker, client, "h.example.org", path); err != nil {
			t.Fatal(err)
		}
	}
	if got := updates.Load(); got != 1 {
		t.Fatalf("expected one update, got %d", got)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected state symlink, got %v, %v", info, err)
	}
}

func TestConcurrentProcessesShareStateLock(t *testing.T) {
	var ipRequests atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ip" {
			if ipRequests.Add(1) == 1 {
				close(started)
				<-release
			}
			fmt.Fprint(w, "203.0.113.7")
			return
		}
		fmt.Fprint(w, "good 203.0.113.7")
	}))
	defer srv.Close()
	defer releaseOnce.Do(func() { close(release) })

	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink("state", alias); err != nil {
		t.Fatal(err)
	}
	checker := ipcheck.New()
	checker.Services = []string{srv.URL + "/ip"}
	client := updater.New(srv.URL+"/update", "user", "key")
	done := make(chan error, 1)
	go func() { done <- run(context.Background(), checker, client, "h.example.org", path) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first process did not start its IP check")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockProcessHelper$")
	cmd.Env = append(os.Environ(), "DYNDNS_LOCK_HELPER=1", "DYNDNS_TEST_URL="+srv.URL, "DYNDNS_TEST_STATE="+alias)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second process failed: %v\n%s", err, output)
	}
	if got := ipRequests.Load(); got != 1 {
		t.Fatalf("second process checked IP before acquiring lock: %d requests", got)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLockProcessHelper(t *testing.T) {
	if os.Getenv("DYNDNS_LOCK_HELPER") != "1" {
		return
	}
	server := os.Getenv("DYNDNS_TEST_URL")
	checker := ipcheck.New()
	checker.Services = []string{server + "/ip"}
	client := updater.New(server+"/update", "user", "key")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := run(ctx, checker, client, "h.example.org", os.Getenv("DYNDNS_TEST_STATE"))
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "acquire state lock") {
		t.Fatalf("expected a lock deadline error, got %v", err)
	}
}

func TestDynRejectionsPersistAcrossRuns(t *testing.T) {
	tests := []struct {
		code      string
		response  string
		temporary bool
	}{
		{"badauth", "badauth", false},
		{"nohost", "nohost", false},
		{"notfqdn", "notfqdn", false},
		{"abuse", "abuse", false},
		{"badagent", "badagent", false},
		{"badsys", "badsys", false},
		{"!yours", "!yours", false},
		{"numhost", "numhost", false},
		{"!donator", "!donator", false},
		{"nohost", "good 203.0.113.7\nnohost", false},
		{"nohost", "good 203.0.113.8\nnohost", false},
		{"911", "911", true},
		{"dnserr", "dnserr", true},
	}
	for _, tt := range tests {
		t.Run(tt.response, func(t *testing.T) {
			var updates atomic.Int32
			var allow atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ip" {
					fmt.Fprint(w, "203.0.113.7")
					return
				}
				updates.Add(1)
				if allow.Load() {
					fmt.Fprint(w, "good 203.0.113.7")
					return
				}
				fmt.Fprint(w, tt.response)
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "state")
			runOnce := func() error {
				checker := ipcheck.New()
				checker.Services = []string{srv.URL + "/ip"}
				return run(context.Background(), checker, updater.New(srv.URL+"/update", "user", "key"), "h.example.org", path)
			}
			before := time.Now()
			if err := runOnce(); err == nil || !strings.Contains(err.Error(), tt.code) {
				t.Fatalf("expected %s rejection, got %v", tt.code, err)
			}
			pause, err := state.LoadPause(path)
			if err != nil || pause.Reason != tt.code {
				t.Fatalf("unexpected persistent pause: %+v, %v", pause, err)
			}
			if tt.temporary {
				if pause.Until.Sub(before) < 30*time.Minute || pause.Until.Sub(time.Now()) > 30*time.Minute {
					t.Fatalf("expected a 30-minute cooldown, got %s", pause.Until)
				}
			} else if !pause.Until.IsZero() {
				t.Fatalf("fatal rejection must require intervention, got %s", pause.Until)
			}
			allow.Store(true)
			for i := 0; i < 2; i++ {
				if err := runOnce(); err == nil || !strings.Contains(err.Error(), "paused") {
					t.Fatalf("expected persistent pause, got %v", err)
				}
			}
			if got := updates.Load(); got != 1 {
				t.Fatalf("retried while paused: %d requests", got)
			}
			if tt.code == "badauth" {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPauseProcessHelper$")
				cmd.Env = append(os.Environ(), "DYNDNS_PAUSE_HELPER=1", "DYNDNS_TEST_URL="+srv.URL, "DYNDNS_TEST_STATE="+path)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("restart failed: %v\n%s", err, output)
				}
				if got := updates.Load(); got != 1 {
					t.Fatalf("restarted process retried while paused: %d requests", got)
				}
			}
			if tt.temporary {
				pause.Until = time.Now().Add(-time.Second)
				if err := state.SavePause(path, pause); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(path + ".blocked"); err != nil {
				t.Fatal(err)
			}
			if err := runOnce(); err != nil {
				t.Fatal(err)
			}
			if got := updates.Load(); got != 2 {
				t.Fatalf("expected update after resuming, got %d requests", got)
			}
			if pause, err := state.LoadPause(path); err != nil || pause.Reason != "" {
				t.Fatalf("pause was not cleared: %+v, %v", pause, err)
			}
		})
	}
}

func TestInterruptedUpdateRemainsPaused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := state.SavePause(path, state.Pause{Reason: "update interrupted or not yet confirmed"}); err != nil {
		t.Fatal(err)
	}
	checker := ipcheck.New()
	checker.Services = nil
	err := run(context.Background(), checker, updater.New("https://example.org", "user", "key"), "h.example.org", path)
	if err == nil || !strings.Contains(err.Error(), "updates paused") {
		t.Fatalf("expected interrupted update to remain paused, got %v", err)
	}
}

func TestStateSaveFailurePreventsRepeatedUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	var updates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ip" {
			fmt.Fprint(w, "203.0.113.7")
			return
		}
		updates.Add(1)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Errorf("create directory to block state replacement: %v", err)
		}
		fmt.Fprint(w, "good 203.0.113.7")
	}))
	defer srv.Close()
	checker := ipcheck.New()
	checker.Services = []string{srv.URL + "/ip"}
	client := updater.New(srv.URL+"/update", "user", "key")
	if err := run(context.Background(), checker, client, "h.example.org", path); err == nil {
		t.Fatal("expected a state save error")
	}
	if err := run(context.Background(), checker, client, "h.example.org", path); err == nil || !strings.Contains(err.Error(), "updates paused") {
		t.Fatalf("expected a persistent pause, got %v", err)
	}
	if got := updates.Load(); got != 1 {
		t.Fatalf("expected one update request, got %d", got)
	}
}

func TestPauseProcessHelper(t *testing.T) {
	if os.Getenv("DYNDNS_PAUSE_HELPER") != "1" {
		return
	}
	server := os.Getenv("DYNDNS_TEST_URL")
	checker := ipcheck.New()
	checker.Services = []string{server + "/ip"}
	err := run(context.Background(), checker, updater.New(server+"/update", "user", "key"), "h.example.org", os.Getenv("DYNDNS_TEST_STATE"))
	if err == nil || !strings.Contains(err.Error(), "updates paused") || !strings.Contains(err.Error(), "badauth") {
		t.Fatalf("expected persistent badauth pause, got %v", err)
	}
}
