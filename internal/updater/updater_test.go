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

package updater

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUpdateOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "user" || p != "key" {
			http.Error(w, "badauth", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("hostname") != "h.example.org" || r.URL.Query().Get("myip") != "203.0.113.7" {
			http.Error(w, "notfqdn", http.StatusBadRequest)
			return
		}
		w.Write([]byte("good 203.0.113.7"))
	}))
	defer srv.Close()

	c := New(srv.URL, "user", "key")
	ans, err := c.Update(context.Background(), "h.example.org", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if ans != "good 203.0.113.7" {
		t.Fatalf("unexpected response %q", ans)
	}
}

func TestUpdateBadAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("badauth"))
	}))
	defer srv.Close()

	c := New(srv.URL, "user", "key")
	if _, err := c.Update(context.Background(), "h.example.org", "203.0.113.7"); err == nil || !strings.Contains(err.Error(), "badauth") {
		t.Fatalf("expected a badauth error, got %v", err)
	}
}

func TestUpdatePreservesQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("token") != "value" || q.Get("hostname") != "h.example.org" || q.Get("myip") != "203.0.113.7" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, "nochg 203.0.113.7")
	}))
	defer srv.Close()

	c := New(srv.URL+"?token=value&hostname=old&myip=old", "user", "key")
	if _, err := c.Update(context.Background(), "h.example.org", "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"http error", http.StatusInternalServerError, "good 203.0.113.7"},
		{"invalid prefix", http.StatusOK, "goodbye"},
		{"mixed results", http.StatusOK, "good 203.0.113.7\nbadauth"},
		{"wrong good IP", http.StatusOK, "good 203.0.113.8"},
		{"wrong nochg IP", http.StatusOK, "nochg 203.0.113.8"},
		{"wrong IP in later response", http.StatusOK, "good 203.0.113.7\nnochg 203.0.113.8"},
		{"invalid IP", http.StatusOK, "good invalid"},
		{"empty", http.StatusOK, ""},
		{"too long", http.StatusOK, "good " + strings.Repeat("x", 1020)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			c := New(srv.URL, "user", "key")
			if _, err := c.Update(context.Background(), "h.example.org", "203.0.113.7"); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestUpdateAcceptsMatchingAndIPLessResponses(t *testing.T) {
	for _, response := range []string{"good", "nochg", "good ::ffff:203.0.113.7", "good 203.0.113.7\nnochg 203.0.113.7"} {
		t.Run(response, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, response)
			}))
			defer srv.Close()
			c := New(srv.URL, "user", "key")
			if _, err := c.Update(context.Background(), "h.example.org", "203.0.113.7"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUpdateRejectsIncompleteResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, "good 203.0.113.7")
	}))
	defer srv.Close()

	c := New(srv.URL, "user", "key")
	if _, err := c.Update(context.Background(), "h.example.org", "203.0.113.7"); err == nil {
		t.Fatal("expected an error reading incomplete response")
	}
}

func TestUpdateErrorsDoNotExposeURLTokens(t *testing.T) {
	const secret = "sensitive-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, r.URL.String())
	}))
	cases := []string{
		srv.URL + "?token=" + secret,
		"://bad-url?token=" + secret,
		srv.URL + "?token=" + secret + "&invalid=%zz",
	}
	for _, baseURL := range cases {
		c := New(baseURL, "user", "key")
		_, err := c.Update(context.Background(), "h.example.org", "203.0.113.7")
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	srv.Close()
	c := New(srv.URL+"?token="+secret, "user", "key")
	_, err := c.Update(context.Background(), "h.example.org", "203.0.113.7")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unexpected network error: %v", err)
	}
}

func TestUpdateDoesNotExposeTokensInMalformedSuccess(t *testing.T) {
	const secret = "sensitive-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "good 203.0.113.7 "+secret)
	}))
	defer srv.Close()
	c := New(srv.URL+"?token="+secret, "user", "key")
	_, err := c.Update(context.Background(), "h.example.org", "203.0.113.7")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unexpected response error: %v", err)
	}
}

func TestUpdateRequestErrorPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New("https://example.org/update?token=secret", "user", "key")
	_, err := c.Update(ctx, "h.example.org", "203.0.113.7")
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
}

func TestUpdateClassifiesDynRejections(t *testing.T) {
	tests := []struct {
		response string
		code     string
		retry    time.Duration
	}{
		{"badauth", "badauth", 0},
		{"badsys", "badsys", 0},
		{"good 203.0.113.7\nnohost", "nohost", 0},
		{"good 203.0.113.8\nnohost", "nohost", 0},
		{"invalid\nbadauth", "badauth", 0},
		{"911", "911", 30 * time.Minute},
		{"dnserr", "dnserr", 30 * time.Minute},
		{"911\nnohost", "nohost", 0},
	}
	for _, tt := range tests {
		t.Run(tt.response, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tt.response)
			}))
			defer srv.Close()
			c := New(srv.URL, "user", "key")
			_, err := c.Update(context.Background(), "h.example.org", "203.0.113.7")
			var rejection *Rejection
			if !errors.As(err, &rejection) || rejection.Code != tt.code || rejection.RetryAfter != tt.retry {
				t.Fatalf("unexpected rejection: %v", err)
			}
		})
	}
}

func TestUpdateClassifiesRejectionFromIncompleteResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, "badauth")
	}))
	defer srv.Close()
	c := New(srv.URL, "user", "key")
	_, err := c.Update(context.Background(), "h.example.org", "203.0.113.7")
	var rejection *Rejection
	if !errors.As(err, &rejection) || rejection.Code != "badauth" {
		t.Fatalf("expected a badauth rejection, got %v", err)
	}
}
