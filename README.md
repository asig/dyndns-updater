# dyndns-updater

Small DynDNS updater in Go (dyndns2 protocol, works with the Dyn Updater Client Key).
Updates the hostname when the public IPv4 address or update target changes.

## Build and test

```bash
make test
make build        # produces bin/dyndns-updater
```

## Usage

```bash
export DYNDNS_HOST="yourhost.dyndns.org"
export DYNDNS_USER="your-dyn-username"
export DYNDNS_PASS="YOUR_UPDATER_CLIENT_KEY"

./bin/dyndns-updater                 # single run (e.g. for cron)
./bin/dyndns-updater -interval 5m    # daemon mode
```

All values can also be set via flags: `-host`, `-user`, `-pass`, `-url`, `-state`, `-interval`.
The last successful update, including the hostname and update URL, is stored in
`~/.dyndns-last-ip` (override with `DYNDNS_STATE`). An older IP-only state file
causes one update before it is replaced with the new format.
State is replaced atomically with owner-only permissions; its directory must be
writable. Existing state files are secured when read. The state file includes
the update URL, including any query parameters.
Concurrent runs using the same state file are serialized with a persistent
adjacent `.lock` file; use the same state file for runs managing the same host.

Fatal DynDNS responses pause updates across restarts. After correcting the
problem, stop the updater and remove the adjacent `.blocked` file (for example,
`~/.dyndns-last-ip.blocked`) to resume. Responses `911` and `dnserr` pause
updates for 30 minutes. An interrupted or unconfirmed update also requires
manual removal of the `.blocked` file before retrying. If the state path is a
symlink, the `.blocked` file is adjacent to its resolved target.

## Running under Supervisor

```bash
sudo make install
sudo cp deploy/supervisor/dyndns-update.conf /etc/supervisor/conf.d/
sudo chmod 600 /etc/supervisor/conf.d/dyndns-update.conf

sudo useradd -r -s /usr/sbin/nologin dyndns
sudo mkdir -p /var/lib/dyndns && sudo chown dyndns: /var/lib/dyndns

sudo supervisorctl reread
sudo supervisorctl update
sudo supervisorctl status dyndns-update
```

Enter hostname, user and key in the `environment=` line of the config.

## Layout

```
cmd/dyndns-updater/  entry point (flags, env, loop, signal handling)
internal/ipcheck/    public IPv4 lookup with fallback services
internal/updater/    dyndns2 request and response handling
internal/state/      read and write the last IP
deploy/supervisor/   Supervisor config
```

## License

Copyright (C) 2026 Andreas Signer asigner@gmail.com

dyndns-updater is free software: you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version. See [LICENSE](LICENSE) for the full text.
