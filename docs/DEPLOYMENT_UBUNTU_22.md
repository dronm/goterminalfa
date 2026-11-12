# Deploy goterminalfa on Ubuntu Server 22.04

This guide installs the USB serial driver as a systemd service. Run the server commands through SSH as a user with `sudo` access. Commands labelled **development computer** run on your own computer; all other commands run on the Ubuntu server.

The examples use the device observed during development. Check the identifiers against your connected terminal before installing the rule or setting its factory serial number.

## 1. Deployment paths

| Purpose | Path or value |
| --- | --- |
| Source directory used in this guide | `$HOME/goterminalfa` |
| Installed executable | `/usr/local/bin/goterminalfa` |
| Application configuration | `/etc/goterminalfa/config.json` |
| Persistent request journal | `/var/lib/goterminalfa/requests.db` |
| Service user and primary group | `terminalfa` |
| Ubuntu serial access group | `dialout` |
| USB device alias | `/dev/terminalfa` |
| udev rule | `/etc/udev/rules.d/99-terminalfa.rules` |
| systemd unit | `/etc/systemd/system/goterminalfa.service` |
| HTTP address used in this guide | `127.0.0.1:8080` |

The source directory is for building. The installed service uses the executable, configuration and state paths above.

## 2. Prepare the server and source

Install the tools used below:

```bash
sudo apt update
sudo apt install --yes ca-certificates curl unzip usbutils udev make python3 psmisc sqlite3 nano
```

On the **development computer**, upload the project archive. Replace `YOUR_SERVER` with the server hostname or IP and `andrey` with your SSH username if different:

```bash
scp goterminalfa.zip andrey@YOUR_SERVER:~/goterminalfa.zip
```

On the server, extract it into a new source directory:

```bash
mkdir -p "$HOME/goterminalfa"
unzip "$HOME/goterminalfa.zip" -d "$HOME/goterminalfa"
cd "$HOME/goterminalfa"
```

The directory must contain `go.mod`, `Makefile`, `cmd/goterminalfa`, and `deploy`.

## 3. Build the production executable

The project requires **Go 1.23 or newer**. Check the installed compiler:

```bash
go version
```

If a suitable compiler is available:

```bash
make prod
./goterminalfa -version
```

If Go is missing or too old, one option on an Ubuntu server is the Go snap:

```bash
sudo apt install --yes snapd
sudo snap install go --classic
/snap/bin/go version
make prod GO_CMD=/snap/bin/go
./goterminalfa -version
```

Use the snap installation commands only if Go is not already installed through snap. For an existing Go snap, use `sudo snap refresh go` instead. An alternative is the official [Go installation procedure](https://go.dev/doc/install).

`make prod` writes `goterminalfa` in the project root. It disables CGO, removes build machine paths, and strips symbol/debug information. The SQLite driver is pure Go; this build does not need SQLite development headers or a C compiler.

Alternatively, build on your **development computer** and upload only the executable. For an x86-64 Ubuntu server, run this in the project source directory on that computer:

```bash
GOOS=linux GOARCH=amd64 make prod
scp goterminalfa andrey@YOUR_SERVER:~/goterminalfa/goterminalfa
```

For an ARM64 server use `GOARCH=arm64`. Check the server architecture with `dpkg --print-architecture`. The remaining steps use the uploaded executable and the project's `deploy` files; Go is not needed to run the installed service.

## 4. Find the USB terminal

Connect and power on the terminal, then list USB devices:

```bash
lsusb
```

The development unit reported USB vendor/product ID **`0483:5740`**. To show only that ID:

```bash
lsusb -d 0483:5740
```

Find the corresponding serial device:

```bash
ls -l /dev/ttyACM* /dev/ttyUSB* /dev/serial/by-id/* 2>/dev/null
sudo journalctl -k -n 80 --no-pager
```

The terminal normally appears as `/dev/ttyACM0`, but its number can change. If the output is unclear, watch the kernel log while unplugging and reconnecting the USB cable:

```bash
sudo journalctl -k -f
```

Press `Ctrl+C` after finding the new `ttyACM` or `ttyUSB` name. In the following commands replace `/dev/ttyACM0` with that actual port.

Inspect the USB attributes:

```bash
udevadm info --attribute-walk --name=/dev/ttyACM0
```

Find the USB parent block containing these attributes. All `ATTRS` matches used together must come from the same parent device:

```text
ATTRS{idVendor}=="0483"
ATTRS{idProduct}=="5740"
ATTRS{serial}=="314"
```

There are three different identifiers in this deployment:

| Identifier | Example | Used for |
| --- | --- | --- |
| USB vendor/product | `0483:5740` | Matching the hardware in udev |
| USB descriptor serial | `314` | Matching the USB device in udev |
| KKT factory serial | `550101046182` | `devices[].expectedSerialNumber` in the application |

The USB serial and KKT factory serial are different values.

If USB is detected but no serial port appears, inspect the kernel messages for `cdc_acm` errors. For this USB CDC device, try loading that driver and reconnecting:

```bash
sudo modprobe cdc_acm
```

## 5. Assign the stable name /dev/terminalfa

From the project root, install the supplied rule:

```bash
cd "$HOME/goterminalfa"
sudo install -o root -g root -m 0644 deploy/99-terminalfa.rules /etc/udev/rules.d/99-terminalfa.rules
sudo nano /etc/udev/rules.d/99-terminalfa.rules
```

The supplied rule is one line:

```udev
SUBSYSTEM=="tty", KERNEL=="ttyACM*", ATTRS{idVendor}=="0483", ATTRS{idProduct}=="5740", ATTRS{serial}=="314", SYMLINK+="terminalfa"
```

Change the vendor, product and USB serial if your inspected values differ. If the actual port is `ttyUSB0`, change `KERNEL=="ttyACM*"` to `KERNEL=="ttyUSB*"`. Save the file.

Reload the rules:

```bash
sudo udevadm control --reload-rules
```

Unplug and reconnect the terminal, then verify the alias and the target permissions:

```bash
udevadm settle
ls -l /dev/terminalfa
readlink -f /dev/terminalfa
ls -lL /dev/terminalfa
```

The alias should resolve to the current serial port, such as `/dev/ttyACM0`. The target should normally be owned by `root:dialout` with mode `0660` (`crw-rw----`). The alias is recreated after reconnection and reboot when the matching device is present.

If you cannot reconnect it physically, ask udev to reprocess the current port instead, using its actual name:

```bash
sudo udevadm trigger --action=add --sysname-match=ttyACM0
udevadm settle
```

For multiple terminals, assign a distinct alias to each, such as `/dev/terminalfa-1` and `/dev/terminalfa-2`. USB descriptor serials such as `314` may be shared by different units. If they are identical, use distinct `/dev/serial/by-path/...` paths tied to physical USB sockets, or matching per-port rules, instead of giving both units the same alias. Configure the correct factory serial for each device as well.

## 6. Create the service account and grant serial access

Create a dedicated account without interactive login:

```bash
if ! id terminalfa >/dev/null 2>&1; then
	sudo useradd --system --user-group --home-dir /var/lib/goterminalfa --shell /usr/sbin/nologin terminalfa
fi
sudo usermod -aG dialout terminalfa
id terminalfa
```

The result must include `dialout`. This is the account that needs USB access when the application runs as a service.

Verify access without sending a command to the terminal:

```bash
sudo -u terminalfa test -r /dev/terminalfa && echo "Serial read permission: OK"
sudo -u terminalfa test -w /dev/terminalfa && echo "Serial write permission: OK"
```

If the target node has a different group or mode, add `GROUP="dialout", MODE="0660"` to the end of your matching udev rule, then reload the rules and reconnect the terminal. Changing the permissions through udev makes them apply on future connections too.

If you also want to run the application manually as your SSH user:

```bash
sudo usermod -aG dialout "$USER"
```

Log out and back in for that membership to apply, or run `newgrp dialout` to start a shell with the new group. Restart an already running service after changing its account's groups.

## 7. Install the executable and configuration

Run from the source directory:

```bash
cd "$HOME/goterminalfa"
sudo install -o root -g root -m 0755 ./goterminalfa /usr/local/bin/goterminalfa
sudo install -d -o root -g terminalfa -m 0750 /etc/goterminalfa
sudo install -d -o terminalfa -g terminalfa -m 0750 /var/lib/goterminalfa
```

Create the configuration for a first installation. If it already exists, edit it in place instead of replacing it with an example:

```bash
if ! sudo test -e /etc/goterminalfa/config.json; then
	sudo install -o root -g terminalfa -m 0640 config.example.json /etc/goterminalfa/config.json
fi
sudo nano /etc/goterminalfa/config.json
```

Use this complete configuration as a starting point:

```json
{
	"http": {
		"listen": "127.0.0.1:8080",
		"readHeaderTimeout": "5s",
		"requestTimeout": "60s",
		"shutdownTimeout": "95s",
		"maxBodyBytes": 65536,
		"bearerToken": ""
	},
	"logging": {
		"level": "info",
		"format": "json",
		"file": ""
	},
	"database": {
		"path": "/var/lib/goterminalfa/requests.db"
	},
	"devices": [
		{
			"id": "cashdesk-1",
			"protocolProfile": "base",
			"expectedSerialNumber": "550101046182",
			"queueSize": 32,
			"connection": {
				"type": "serial",
				"port": "/dev/terminalfa",
				"baudRate": 115200,
				"responseTimeout": "25s"
			}
		}
	]
}
```

Adjust these fields:

- `devices[].connection.port`: the complete stable device path created above.
- `devices[].expectedSerialNumber`: your KKT's 12-digit factory serial as a JSON string. The example is the development unit. If unknown, temporarily use `""`, read the actual serial in step 9, then set the confirmed value and restart.
- `devices[].id`: the logical ID your calling application uses. Keep it assigned to the same physical KKT across upgrades because stored request identities are scoped by this ID.
- `devices[].protocolProfile`: `base` matches the original configuration. Use `ffd1.2` for the supported FFD 1.2 commands and the composite `createReceipt` workflow. Choose the profile matching the device and your caller's commands; selecting it does not change the KKT's registration.
- `http.listen`: `127.0.0.1:8080` accepts connections from the same server. Use this address when the calling backend runs on that server.
- `http.bearerToken`: an empty string disables API authentication. To require authentication, set a token and pass `Authorization: Bearer YOUR_TOKEN` in the calling application and API checks. For access from another host, use a private interface or an HTTPS reverse proxy and configure authentication; the application listener itself serves HTTP.

Keep `database.path` absolute as shown. Relative database and log paths are resolved from the **configuration file's directory**, not `WorkingDirectory`. A copied `./data/requests.db` would therefore point under `/etc/goterminalfa`, where the service account cannot normally write. An empty `logging.file` sends logs to the systemd journal.

The 60-second request and 95-second shutdown settings allow more time for receipt workflows and their cleanup. The unit below has a 105-second stop timeout. Increase the budgets together if your operations require longer.

Check JSON syntax and permissions:

```bash
sudo python3 -m json.tool /etc/goterminalfa/config.json >/dev/null
sudo chown root:terminalfa /etc/goterminalfa/config.json
sudo chmod 0640 /etc/goterminalfa/config.json
sudo -u terminalfa test -r /etc/goterminalfa/config.json && echo "Config read permission: OK"
sudo -u terminalfa test -w /var/lib/goterminalfa && echo "State directory write permission: OK"
```

`json.tool` checks JSON syntax only. The application checks field names and values when it starts.

The production database is created by the service. Keep the archive's development `data/` files separate from this production state.

## 8. Install and start the systemd service

Install the supplied unit:

```bash
cd "$HOME/goterminalfa"
sudo install -o root -g root -m 0644 deploy/goterminalfa.service /etc/systemd/system/goterminalfa.service
```

The complete unit is:

```ini
[Unit]
Description=Terminal FA HTTP driver
After=network.target

[Service]
Type=simple
User=terminalfa
Group=terminalfa
SupplementaryGroups=dialout
StateDirectory=goterminalfa
StateDirectoryMode=0750
WorkingDirectory=/var/lib/goterminalfa
ExecStart=/usr/local/bin/goterminalfa -config /etc/goterminalfa/config.json
Restart=on-failure
RestartSec=5s
TimeoutStopSec=105s
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

`StateDirectory` creates and maintains `/var/lib/goterminalfa` for the service account. It preserves the directory when the service stops. `ProtectHome=true` is why the installed binary, configuration and database use paths outside home directories. `SupplementaryGroups=dialout` grants serial access in the service process.

Load and start it, and enable automatic startup after reboot:

```bash
sudo systemd-analyze verify /etc/systemd/system/goterminalfa.service
sudo systemctl daemon-reload
sudo systemctl enable --now goterminalfa.service
sudo systemctl status goterminalfa.service --no-pager --full
sudo journalctl -u goterminalfa.service -n 100 --no-pager
```

For live logs:

```bash
sudo journalctl -u goterminalfa.service -f
```

Press `Ctrl+C` to stop following logs; the service continues running.

Check the server clock and timezone before using application commands that copy server time to the KKT:

```bash
timedatectl status
```

If the KKT uses Tyumen local time, for example, set that timezone:

```bash
sudo timedatectl set-timezone Asia/Yekaterinburg
sudo systemctl restart goterminalfa.service
```

Choose the timezone used at the actual terminal location.

## 9. Verify the process and physical device

First check process/database health:

```bash
curl --fail-with-body --silent --show-error --max-time 5 \
	http://127.0.0.1:8080/healthz
```

Expected response:

```json
{"status":"ok"}
```

This checks the process and SQLite journal. It does **not** open the USB port. The application opens devices lazily, so a successful startup also does not prove that a terminal is connected.

List the configured devices:

```bash
curl --fail-with-body --silent --show-error --max-time 5 \
	http://127.0.0.1:8080/api/v1/devices
```

Then read the physical terminal's factory serial:

```bash
curl --fail-with-body --silent --show-error --max-time 90 \
	-H 'Content-Type: application/json' \
	--data '{"deviceId":"cashdesk-1","cmd":"getSerialNumber","params":{}}' \
	http://127.0.0.1:8080/api/v1/commands
```

A successful result contains `"status":"completed"` and `data.serialNumber`. Compare that returned factory serial with the intended terminal and the value in `expectedSerialNumber`. Both the serial-number and status reads check a nonempty `expectedSerialNumber`: a mismatch returns `502 device_identity_mismatch`, with the expected and actual serials in the error message. If discovering an unknown factory serial, temporarily set `expectedSerialNumber` to `""`, restart, read the serial, then save the confirmed value and restart again.

Read the device status:

```bash
curl --fail-with-body --silent --show-error --max-time 90 \
	-H 'Content-Type: application/json' \
	--data '{"deviceId":"cashdesk-1","cmd":"getStatus","params":{}}' \
	http://127.0.0.1:8080/api/v1/commands
```

These two commands only read from the device and do not require a `requestId`. They do not open a shift or create a receipt. Use your configured `deviceId` if you changed it.

If a bearer token is configured, add this option to every `/api/v1/*` request, replacing the placeholder with the configured value:

```bash
-H 'Authorization: Bearer YOUR_TOKEN'
```

For example:

```bash
curl --fail-with-body --silent --show-error --max-time 5 \
	-H 'Authorization: Bearer YOUR_TOKEN' \
	http://127.0.0.1:8080/api/v1/devices
```

`/healthz` remains available without a token.

After editing the configuration, apply it with:

```bash
sudo systemctl restart goterminalfa.service
```

## 10. Routine operation and upgrades

Useful commands:

```bash
sudo systemctl start goterminalfa.service
sudo systemctl stop goterminalfa.service
sudo systemctl restart goterminalfa.service
sudo systemctl is-enabled goterminalfa.service
sudo systemctl is-active goterminalfa.service
sudo journalctl -u goterminalfa.service --since today --no-pager
```

For an upgrade, build or upload the new `./goterminalfa` executable first. Then stop the service, back up the current executable, configuration and request journal, and install the new binary:

```bash
cd "$HOME/goterminalfa"
sudo systemctl stop goterminalfa.service
terminalfa_backup_dir="/var/backups/goterminalfa/$(date -u +%Y%m%dT%H%M%SZ)"
sudo install -d -o root -g root -m 0700 "$terminalfa_backup_dir"
sudo cp -p /usr/local/bin/goterminalfa "$terminalfa_backup_dir/goterminalfa"
sudo cp -p /etc/goterminalfa/config.json "$terminalfa_backup_dir/config.json"
sudo sqlite3 /var/lib/goterminalfa/requests.db ".backup '$terminalfa_backup_dir/requests.db'"
sudo chmod 0600 "$terminalfa_backup_dir/requests.db"
sudo install -o root -g root -m 0755 ./goterminalfa /usr/local/bin/goterminalfa
sudo systemctl start goterminalfa.service
sudo systemctl status goterminalfa.service --no-pager --full
```

Run each command only after the preceding one succeeds. If a backup or installation fails, resolve it before continuing. Repeat the health and device reads from step 9 after the upgrade.

The SQLite database contains request history used to prevent duplicate execution. Preserve it across deployments. SQLite uses WAL: use SQLite's backup command as above instead of copying only a live `requests.db` file or discarding its WAL. A remaining `requests.db.lock` file is normal; the running process's file lock determines ownership.

Replacing only the binary or editing JSON needs a service restart. After changing the unit, run `sudo systemctl daemon-reload` before restarting. After changing a udev rule, reload udev rules and reconnect or retrigger the device.

## 11. Troubleshooting

| Symptom | Check and action |
| --- | --- |
| `/dev/terminalfa` is missing | Find the real `ttyACM`/`ttyUSB` node, inspect `udevadm info`, compare every rule match, reload rules and reconnect. The alias only exists while the matching hardware is present. |
| USB appears in `lsusb`, but no serial node appears | Read kernel logs, check cable/power and `cdc_acm` loading. Confirm that the connected interface exposes a serial port. |
| `permission denied` opening the serial port | Run `ls -lL /dev/terminalfa`, `id terminalfa` and the read/write permission checks from step 6. Ensure the target group is `dialout` and restart the service after group changes. |
| SQLite `permission denied` or a path under `/etc/goterminalfa/data` | Set the absolute database path from step 7 and ensure `/var/lib/goterminalfa` belongs to `terminalfa`. |
| Service fails with `203/EXEC` or `Exec format error` | Check `/usr/local/bin/goterminalfa`, its executable permissions, and whether the binary was built for the server's OS/CPU architecture. |
| Service fails with `217/USER` | Check that the `terminalfa` user/group exist and that `dialout` exists. |
| `address already in use` | Run `sudo ss -ltnp 'sport = :8080'` and check for a manual instance or another service using that port. |
| Process lock error | Stop the other process using the same request journal. Do not delete a lock file while another process is running. |
| `401 unauthorized` | Supply the exact configured bearer token in the `Authorization` header. |
| `404 unknown_device` | Match the request's `deviceId` to `devices[].id` in the active configuration. |
| `502 transport_error` | Check that the alias exists, its permissions, cable/power, and whether another program is using the port. |
| `504 device_timeout` | Check the terminal's serial control mode and baud rate, USB connection and other users of the serial port. |
| `502 device_identity_mismatch` | Check the physical KKT identity, USB alias and configured factory serial. Serial-number and status reads also check a nonempty expected serial; use the discovery procedure in step 9 if the factory serial is unknown. |
| Health succeeds but a device read fails | Health tests SQLite and HTTP only; diagnose the USB connection separately. |

To find processes with the real serial port open:

```bash
sudo fuser -v /dev/ttyACM0
```

Use the current target from `readlink -f /dev/terminalfa`. The driver opens the port for an operation and closes it afterward, so an idle process may not appear in this output. Run one driver service per physical terminal and avoid concurrent manual instances.

If logs or `fuser` show that ModemManager is probing this terminal, append `ENV{ID_MM_DEVICE_IGNORE}="1"` to the terminal's matching udev rule, then reload rules and reconnect. Apply this only to the identified terminal's rule.

For detailed protocol diagnostics, temporarily set `logging.level` to `debug`, restart, and inspect the journal. Restore `info` afterward; debug logs include raw protocol frames.

## References

- [Project service unit](../deploy/goterminalfa.service)
- [Project udev rule](../deploy/99-terminalfa.rules)
- [HTTP API](../docs/API.md)
- [FFD 1.2 profile](../docs/FFD12.md)
- [Ubuntu 22.04 udev manual](https://manpages.ubuntu.com/manpages/jammy/en/man7/udev.7.html)
- [Ubuntu 22.04 udevadm manual](https://manpages.ubuntu.com/manpages/jammy/en/man8/udevadm.8.html)
- [Ubuntu 22.04 systemd execution settings](https://manpages.ubuntu.com/manpages/jammy/en/man5/systemd.exec.5.html)
- [Ubuntu Go setup guide](https://ubuntu.com/developers/docs/howto/go-setup/)
- [Official Go installation](https://go.dev/doc/install)
