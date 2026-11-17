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

// Package ipcheck determines the public IPv4 address.
package ipcheck

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultServices are the fallback services, tried in order.
var DefaultServices = []string{
	"https://api.ipify.org",
	"https://ipv4.icanhazip.com",
	"https://ifconfig.me/ip",
}

// Checker asks services for the public IP.
type Checker struct {
	Services []string
	Client   *http.Client
}

// New returns a Checker with the default services.
func New() *Checker {
	return &Checker{
		Services: DefaultServices,
		Client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// PublicIP returns the first valid IPv4 from any of the services.
func (c *Checker) PublicIP(ctx context.Context) (string, error) {
	var lastErr error = fmt.Errorf("no services configured")
	for _, svc := range c.Services {
		ip, err := c.query(ctx, svc)
		if err == nil {
			return ip, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func (c *Checker) query(ctx context.Context, svc string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, svc, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d", svc, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	p := net.ParseIP(ip)
	if p == nil || p.To4() == nil {
		return "", fmt.Errorf("%s: invalid IPv4 %q", svc, ip)
	}
	return p.To4().String(), nil
}
