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

package ipcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicIPFallback(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("203.0.113.7\n"))
	}))
	defer good.Close()

	c := New()
	c.Services = []string{bad.URL, good.URL}

	ip, err := c.PublicIP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.7" {
		t.Fatalf("expected 203.0.113.7, got %q", ip)
	}
}

func TestPublicIPInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-an-ip"))
	}))
	defer srv.Close()

	c := New()
	c.Services = []string{srv.URL}
	if _, err := c.PublicIP(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestPublicIPNormalizesMappedIPv4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("::ffff:203.0.113.7\n"))
	}))
	defer srv.Close()

	c := New()
	c.Services = []string{srv.URL}
	ip, err := c.PublicIP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.7" {
		t.Fatalf("expected 203.0.113.7, got %q", ip)
	}
}
