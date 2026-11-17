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
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/asig/dyndns-updater/internal/ipcheck"
	"github.com/asig/dyndns-updater/internal/state"
	"github.com/asig/dyndns-updater/internal/updater"
	"github.com/gofrs/flock"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, ic *ipcheck.Checker, up *updater.Client, host, stateFile string) (err error) {
	stateFile, err = state.ResolvePath(stateFile)
	if err != nil {
		return err
	}
	lock := flock.New(stateFile + ".lock")
	locked, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquire state lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("acquire state lock: lock not acquired")
	}
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("release state lock: %w", unlockErr))
		}
	}()

	pause, err := state.LoadPause(stateFile)
	if err != nil {
		return err
	}
	if pause.Reason != "" {
		if pause.Until.IsZero() {
			return fmt.Errorf("updates paused (%s); after correcting the problem, remove %q to resume", pause.Reason, stateFile+".blocked")
		}
		if time.Now().Before(pause.Until) {
			return fmt.Errorf("updates paused (%s) until %s", pause.Reason, pause.Until.Format(time.RFC3339))
		}
		if err := state.ClearPause(stateFile); err != nil {
			return err
		}
	}

	ip, err := ic.PublicIP(ctx)
	if err != nil {
		return fmt.Errorf("failed to determine public IP: %w", err)
	}
	last, err := state.Load(stateFile)
	if err != nil {
		return err
	}
	current := state.Record{Host: host, URL: up.BaseURL, IP: ip}
	if last == current {
		return nil
	}
	if err := state.SavePause(stateFile, state.Pause{Reason: "update interrupted or not yet confirmed"}); err != nil {
		return err
	}
	answer, err := up.Update(ctx, host, ip)
	if err != nil {
		var rejection *updater.Rejection
		if errors.As(err, &rejection) {
			pause := state.Pause{Reason: rejection.Code}
			if rejection.RetryAfter > 0 {
				pause.Until = time.Now().Add(rejection.RetryAfter).UTC()
			}
			if pauseErr := state.SavePause(stateFile, pause); pauseErr != nil {
				return errors.Join(err, pauseErr)
			}
			if pause.Until.IsZero() {
				return fmt.Errorf("%w; updates paused until the problem is corrected and %q is removed", err, stateFile+".blocked")
			}
			return fmt.Errorf("%w; updates paused until %s", err, pause.Until.Format(time.RFC3339))
		}
		if clearErr := state.ClearPause(stateFile); clearErr != nil {
			return errors.Join(err, clearErr)
		}
		return err
	}
	if err := state.Save(stateFile, current); err != nil {
		return err
	}
	if err := state.ClearPause(stateFile); err != nil {
		return err
	}
	log.Printf("OK: %s -> %s (%s)", host, ip, answer)
	return nil
}

func main() {
	home, _ := os.UserHomeDir()
	host := flag.String("host", env("DYNDNS_HOST", ""), "hostname")
	user := flag.String("user", env("DYNDNS_USER", ""), "username")
	pass := flag.String("pass", env("DYNDNS_PASS", ""), "updater client key")
	baseURL := flag.String("url", env("DYNDNS_URL", "https://members.dyndns.org/nic/update"), "update URL")
	stateFile := flag.String("state", env("DYNDNS_STATE", home+"/.dyndns-last-ip"), "state file")
	interval := flag.Duration("interval", 0, "interval in daemon mode (e.g. 5m), 0 = run once")
	flag.Parse()

	if *host == "" || *user == "" || *pass == "" {
		log.Fatal("host, user and pass must be set (flags or DYNDNS_HOST/DYNDNS_USER/DYNDNS_PASS)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ic := ipcheck.New()
	up := updater.New(*baseURL, *user, *pass)

	if *interval <= 0 {
		if err := run(ctx, ic, up, *host, *stateFile); err != nil {
			log.Fatalf("ERROR: %v", err)
		}
		return
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		if err := run(ctx, ic, up, *host, *stateFile); err != nil {
			log.Printf("ERROR: %v", err)
		}
		select {
		case <-ctx.Done():
			log.Print("stopped")
			return
		case <-ticker.C:
		}
	}
}
