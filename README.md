# goterminalfa

Go HTTP/JSON driver service for **ККТ Терминал-ФА**. Applications address configured devices by logical ID; the service exchanges the binary KKT protocol over USB serial or raw TCP.

The command catalogue covers **all 72 entries listed in the supplied base protocol: 68 implemented and four explicitly unavailable** because their payload formats are not defined. Status and identification, registration, shifts, receipts, correction receipts, archives, printing, cash counters, and device settings are included. Two application commands bring discovery to **74 entries**: **`executeOperation`** runs complete sequences under one device lock and request ID, and **`syncDateTime`** sets the KKT clock from the Go server's local wall clock.

- Multiple independently configured devices, with a bounded waiting queue and exclusive execution per device.
- Serial transport with 115200 baud and 8N1 by default; connections open for an operation and close afterward.
- Per-device `serial` or `tcp` transport selection, including mixed devices in one server. TCP opens a new connection for each protocol command.
- Logrus structured logging, with JSON or text output.
- JSON configuration and SQLite persistence for idempotent state-changing operations.
- A command catalogue containing parameter metadata, descriptions, and documentation references.

Identification and diagnostic reads are safe to repeat and **do not create SQLite request records**, even when a `requestId` is supplied. Commands marked as state-changing require a `requestId`; that includes printing and archive cursor selection/advancement, not just fiscal document creation. See [docs/COMMANDS.md](docs/COMMANDS.md) for the complete index and [docs/commands.json](docs/commands.json) for the exported catalogue.

## Build and run

Use **Go 1.23 or newer** on Linux. SQLite uses the pure-Go `modernc.org/sqlite` driver; building the server does not require a C compiler or a separately installed SQLite development package.

```bash
go mod download
go build -o bin/goterminalfa ./cmd/goterminalfa
cp config.example.json config.json
```

Edit `config.json` for your device, then start the server:

```bash
./bin/goterminalfa -config ./config.json
```

For development:

```bash
go run ./cmd/goterminalfa -config ./config.json
```

The example listens on `127.0.0.1:8080`. The database and log-file paths, when relative, are resolved against the directory containing the configuration file.

## Serial access on Linux

Check the detected port and its group:

```bash
ls -l /dev/ttyACM* /dev/serial/by-id/* 2>/dev/null
```

On Arch Linux:

```bash
sudo usermod -aG uucp "$USER"
newgrp uucp
```

On Ubuntu:

```bash
sudo usermod -aG dialout "$USER"
newgrp dialout
```

Run the program from the new shell. For a systemd service, grant access to its service account and restart the service.

Prefer an existing `/dev/serial/by-id/...` path or a custom udev alias to `/dev/ttyACM0`, whose number can change after reconnection or reboot.

For the device identified during development, install the supplied example rule:

```bash
sudo install -m 0644 deploy/99-terminalfa.rules /etc/udev/rules.d/99-terminalfa.rules
sudo udevadm control --reload-rules
```

Unplug and reconnect the device, then check:

```bash
ls -l /dev/terminalfa
```

The rule matches USB vendor `0483`, product `5740`, and USB descriptor serial `314`. Adjust it for your hardware. **USB serial `314` and KKT factory serial `550101046182` are different identifiers.** Do not assume that USB serials are unique across all devices. Give each configured device a unique stable path and, when known, its expected factory serial number.

## Configuration

See [config.example.json](config.example.json) for the serial example, [config.tcp.example.json](config.tcp.example.json) for TCP, and [config.mixed.example.json](config.mixed.example.json) for both transports in one process. A complete serial configuration has this shape:

```json
{
	"http": {
		"listen": "127.0.0.1:8080",
		"readHeaderTimeout": "5s",
		"requestTimeout": "30s",
		"shutdownTimeout": "35s",
		"maxBodyBytes": 65536,
		"bearerToken": ""
	},
	"logging": {
		"level": "info",
		"format": "json",
		"file": ""
	},
	"database": {
		"path": "./data/requests.db"
	},
	"devices": [
		{
			"id": "cashdesk-1",
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

Add entries to `devices` for additional KKT units. IDs, serial paths or TCP endpoints, and supplied expected serial numbers must identify distinct devices. Keep each logical ID assigned to the same physical KKT across restarts; persisted operation identities are scoped by that ID. `expectedSerialNumber` is optional; when set, a mismatching serial-number response is rejected.

For TCP, the connection object uses `type: "tcp"`, `address`, `connectTimeout`, and `responseTimeout`. `address` is `host:port`, for example `192.0.2.50:7778`, a hostname with a port, or `[2001:db8::50]:7778`; it has no URL scheme. Port `7778` is only an example. Read the actual KKT command port with `getControlSettings` (`0x81`, tag `30032`).

TCP `connectTimeout` bounds dialing; `responseTimeout` bounds the whole exchange, including connect, write, and read. Serial `responseTimeout` supplies each exchange deadline and cancellation interrupts response reads; synchronous serial opening, configuration, and writes cannot be forcibly interrupted by that deadline. HTTP `requestTimeout` includes queue waiting and all commands in a complete operation. Increase it, the HTTP client's timeout, and `shutdownTimeout` for longer receipt or archive sequences. Independent devices can execute concurrently.

Use [docs/LAN.md](docs/LAN.md) to prepare a KKT for LAN control. The device uses raw binary TCP, not HTTP; application HTTP/JSON requests remain identical. A TCP operation retains exclusive device ownership across all of its separate command connections. Serial operations retain one open port for the full sequence.

`logging.file` empty means standard error, which systemd captures in the journal. `debug` logging includes hexadecimal protocol frames. Set `http.bearerToken` to require an `Authorization: Bearer ...` header for `/api/v1/*` access; `/healthz` remains available without authentication.

## Read a serial number

```bash
curl --fail-with-body --silent --show-error \
	-H 'Content-Type: application/json' \
	--data '{"deviceId":"cashdesk-1","cmd":"getSerialNumber","params":{}}' \
	http://127.0.0.1:8080/api/v1/commands
```

Successful response:

```json
{
	"deviceId": "cashdesk-1",
	"cmd": "getSerialNumber",
	"status": "completed",
	"data": {
		"serialNumber": "550101046182"
	}
}
```

The alias is equivalent:

```bash
curl --fail-with-body --silent --show-error \
	-H 'Content-Type: application/json' \
	--data '{"deviceId":"cashdesk-1","cmd":"ID","params":{}}' \
	http://127.0.0.1:8080/api/v1/commands
```

When a bearer token is configured, add `-H 'Authorization: Bearer YOUR_TOKEN'` to API requests. The serial number above is an example from the tested device; your unit returns its own number.

`deviceId` and any nonempty `requestId` must match `[A-Za-z0-9][A-Za-z0-9_.-]{0,127}`. Requests reject unknown envelope fields and duplicate JSON object keys, including keys inside `params`.

More read-only request examples are provided for [status](examples/read-status.json), [firmware](examples/read-firmware.json), [clock](examples/read-clock.json), and [cash counters](examples/read-cash-counters.json). They use the same endpoint as the serial-number query. [examples/README.md](examples/README.md) also describes taking one journaled snapshot of all device settings.

## Synchronize the device clock

Submit `cmd: "syncDateTime"`, an empty `params` object, and a required request ID; [examples/sync-clock.json](examples/sync-clock.json) provides the request. The command samples the Go server's local time when execution begins after queue waiting and any configured identity verification, drops seconds, and writes the normal `setDateTime` (`0x72`, TLV tag `30000`) value. A successful response contains `data.dateTime` in `YYYY-MM-DDTHH:mm` form. Serial and TCP devices use the same request.

Set the server's clock and process timezone correctly. For example:

```bash
TZ=Asia/Yekaterinburg ./bin/goterminalfa -config ./config.json
```

The sampled time comes from that process, not from the HTTP caller or browser. Repeating the same request ID replays the previous result and does not synchronize again; use a new request ID for each intended synchronization. No schedule or automatic synchronization is created.

## Stateful operations

Each state-changing request needs a stable `requestId`. Send a complete multi-command workflow with `cmd: "executeOperation"`; commands such as `beginReceipt`, `addReceiptItem`, and `finalizeReceipt` cannot be sent as independent HTTP calls. The whole sequence is validated before communication and runs without another request interleaving on that device.

The examples directory includes JSON templates for [setting the clock](examples/set-clock.json), [opening a complete shift](examples/open-shift.json), [creating a complete receipt](examples/create-receipt.json), and [reading an archive document](examples/read-document-tlv.json). **These are templates, not automatically run scripts.** Clock, shift, and receipt examples change device state; edit their identities and parameters for the intended registered device before deliberately submitting them. A new intended operation needs a new request ID; retrying the same request ID retrieves its saved result instead of executing it again.

Settings writes use complete named forms, not patch semantics. Network settings require the address-mode flag and all three IPv4 fields even with DHCP. OFD settings require a domain, port, and timer; only the fallback IP is optional. Printer settings require font size, retract, and extra text; only the model is optional. Control settings require both interface and TCP port. [The API guide](docs/API.md#device-settings-and-cash-counters) explains the exact forms and cash operations.

Selecting Ethernet with `setControlSettings` changes the KKT's control interface; it does not change the server's per-device transport configuration. Such a switch must be the final step of an `executeOperation`. For a device already using TCP, any network-settings or control-settings write must be the final batch step because its address, port, or interface may change. Set that device's connection type/address separately and restart the server when migrating. The supplied control-setting request template continues to select serial mode `0`.

Read [docs/API.md](docs/API.md) for the full HTTP contract, workflows, exact numeric formats, and uncertain outcomes. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) explains execution and persistence details.

## Validation

```bash
go test ./...
go vet ./...
go build ./cmd/goterminalfa
```

For race detection, run `go test -race ./...` on a supported platform with the Go race detector's C compiler requirements installed.

Tests use simulated exchanges and temporary SQLite databases. They do not issue commands to your physical KKT. Repeat the serial-number request above to verify your hardware connection; the supplied mutating examples are not hardware acceptance tests.

## Ubuntu service

An example systemd unit is supplied in [deploy/goterminalfa.service](deploy/goterminalfa.service). Build the binary, then prepare its account and directories:

```bash
sudo useradd --system --user-group --home-dir /var/lib/goterminalfa --shell /usr/sbin/nologin terminalfa
sudo usermod -aG dialout terminalfa
sudo install -m 0755 bin/goterminalfa /usr/local/bin/goterminalfa
sudo install -d -o root -g terminalfa -m 0750 /etc/goterminalfa
sudo install -o root -g terminalfa -m 0640 config.example.json /etc/goterminalfa/config.json
```

Edit `/etc/goterminalfa/config.json`: set the correct device path and identity, and use `/var/lib/goterminalfa/requests.db` for `database.path`. Leave `logging.file` empty to use the journal.

```bash
sudo install -m 0644 deploy/goterminalfa.service /etc/systemd/system/goterminalfa.service
sudo systemctl daemon-reload
sudo systemctl enable --now goterminalfa
sudo journalctl -u goterminalfa -f
```

The unit creates `/var/lib/goterminalfa` through `StateDirectory`. On Arch, change `SupplementaryGroups=dialout` to `SupplementaryGroups=uucp` and grant that group to the account.

## Protocol reference

Definitions follow **«Протокол информационного обмена ККТ Терминал-ФА / ККТ КИТ Онлайн-Ф», version 01.05**, including its appendix-10 additions. Command metadata records page references, field constraints, notes, and prerequisites. The [manufacturer's product page](https://kit-invest.ru/TerminalFA) links to its protocol and operating manual.

The four unavailable commands (`0x14`, `0x15`, `0x17`, `0x34`) return HTTP `501`. The separate FFD 1.2 supplement contains conflicting fiscal-storage closure codes, so those definitions are not silently mixed into the base protocol. The driver does not automatically retry device commands or fail over between transports.
