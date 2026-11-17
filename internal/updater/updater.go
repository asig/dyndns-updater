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

// Package updater implements the dyndns2 update protocol.
package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Client sends updates to a dyndns2-compatible endpoint.
type Client struct {
	BaseURL string
	User    string
	Pass    string
	HTTP    *http.Client
}

type requestError struct {
	operation string
	cause     error
}

func (e *requestError) Error() string {
	switch {
	case errors.Is(e.cause, context.Canceled):
		return e.operation + ": canceled"
	case errors.Is(e.cause, context.DeadlineExceeded):
		return e.operation + ": deadline exceeded"
	}
	var netErr net.Error
	if errors.As(e.cause, &netErr) && netErr.Timeout() {
		return e.operation + ": timed out"
	}
	var dnsErr *net.DNSError
	if errors.As(e.cause, &dnsErr) {
		if dnsErr.IsNotFound {
			return e.operation + ": DNS name not found"
		}
		return e.operation + ": DNS lookup failed"
	}
	var errno syscall.Errno
	if errors.As(e.cause, &errno) {
		return e.operation + ": " + errno.Error()
	}
	cause := e.cause
	var urlErr *url.Error
	if errors.As(cause, &urlErr) {
		cause = urlErr.Err
	}
	return fmt.Sprintf("%s: %T", e.operation, cause)
}

func (e *requestError) Unwrap() error { return e.cause }

// Rejection identifies a DynDNS response that requires updates to be paused.
// RetryAfter is zero when manual intervention is required.
type Rejection struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *Rejection) Error() string {
	return fmt.Sprintf("update rejected (HTTP %d): %s", e.Status, e.Code)
}

func unexpectedResponse(status int, answer string) error {
	var temporary *Rejection
	for _, line := range strings.Split(answer, "\n") {
		code := strings.TrimSpace(line)
		switch code {
		case "badauth", "nohost", "notfqdn", "!yours", "abuse", "badagent", "badsys", "numhost", "!donator":
			return &Rejection{Status: status, Code: code}
		case "dnserr", "911":
			temporary = &Rejection{Status: status, Code: code, RetryAfter: 30 * time.Minute}
		}
	}
	if temporary != nil {
		return temporary
	}
	return fmt.Errorf("unexpected update response (HTTP %d)", status)
}

// New returns a Client with a sensible timeout.
func New(baseURL, user, pass string) *Client {
	return &Client{
		BaseURL: baseURL,
		User:    user,
		Pass:    pass,
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Update sets host to ip and returns the server response (good/nochg ...).
func (c *Client) Update(ctx context.Context, host, ip string) (string, error) {
	updateURL, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", &requestError{operation: "invalid update URL", cause: err}
	}
	q, err := url.ParseQuery(updateURL.RawQuery)
	if err != nil {
		return "", &requestError{operation: "invalid update URL query", cause: err}
	}
	q.Set("hostname", host)
	q.Set("myip", ip)
	updateURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateURL.String(), nil)
	if err != nil {
		return "", &requestError{operation: "create update request", cause: err}
	}
	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("User-Agent", "go-dyndns-updater/1.0")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", &requestError{operation: "update request failed", cause: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1025))
	answer := strings.TrimSpace(string(body))
	responseErr := unexpectedResponse(resp.StatusCode, answer)
	var rejection *Rejection
	if errors.As(responseErr, &rejection) {
		if err != nil {
			return answer, errors.Join(responseErr, &requestError{operation: "read update response", cause: err})
		}
		return answer, responseErr
	}
	if err != nil {
		return "", &requestError{operation: "read update response", cause: err}
	}
	if len(body) > 1024 {
		return "", fmt.Errorf("update response exceeds 1024 bytes")
	}

	if resp.StatusCode != http.StatusOK || answer == "" {
		return answer, responseErr
	}
	for _, line := range strings.Split(answer, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || len(fields) > 2 || (fields[0] != "good" && fields[0] != "nochg") {
			return answer, responseErr
		}
		if len(fields) > 1 {
			returnedIP, requestedIP := net.ParseIP(fields[1]), net.ParseIP(ip)
			if returnedIP == nil || requestedIP == nil {
				return answer, fmt.Errorf("update response contains an invalid IP")
			}
			if !returnedIP.Equal(requestedIP) {
				return answer, fmt.Errorf("update response IP %q does not match requested IP %q", fields[1], ip)
			}
		}
	}
	return answer, nil
}
