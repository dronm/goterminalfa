# goterminalfa

Go HTTP/JSON driver service for **ККТ Терминал-ФА**. Applications address configured devices by logical ID; the service exchanges the binary KKT protocol over USB serial or raw TCP.

The default **`base` protocol profile** covers **all 72 entries listed in the supplied base protocol: 68 implemented and four explicitly unavailable** because their payload formats are not defined. Three application commands bring that catalogue to **75 entries**. **`executeOperation`** runs complete sequences under one device lock and request ID, **`syncDateTime`** sets the KKT clock from the server's local time, and **`closeShiftIfOpen`** closes an open shift. An explicit per-device **`ffd1.2`** profile selects the implemented supplement formats and adds **`createReceipt`** and **`createCorrectionReceipt`**, which conditionally open a shift before creating an unmarked normal or correction receipt and manage cleanup of a document they confirmed opening. The FFD catalogue has **93 entries**: **77 implemented public wire definitions** covering **76 distinct command bytes**, **10 explicitly unavailable entries**, and six application commands. FFD-only **`registerKkt`** executes registration/re-registration as one `0xE1` → `0xE2` → `0xE3` operation and validates the registration number against the actual KKT factory serial before starting it. Two named report finalizers share `0x28`. The catalogue also includes **`applyActivationCode`** (`0xDD`) for submitting a vendor-supplied activation code; full marked-goods support remains outside scope.

- Multiple independently configured devices, with a bounded waiting queue and exclusive execution per device.
- Serial transport with 115200 baud and 8N1 by default; connections open for an operation and close afterward.
- Per-device `serial` or `tcp` transport selection, including mixed devices in one server. TCP opens a new connection for each protocol command.
- Per-device protocol profiles, independent of transport: `base` by default, or explicitly selected `ffd1.2`.
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

## Protocol profiles

Omitting a device's `protocolProfile` preserves the `base` formats and existing HTTP behavior. Set `"protocolProfile": "ffd1.2"` alongside that device's `id` and `connection` to select the supported supplement definitions. The setting is independent of serial/TCP transport. [config.ffd12.example.json](config.ffd12.example.json) configures the example logical device `cashdesk-ffd12` with a longer request timeout for complete operations.

Use `GET /api/v1/commands?deviceId=cashdesk-ffd12` to inspect that device's actual catalogue. Bare `GET /api/v1/commands` retains the base catalogue. Device discovery includes the selected `protocolProfile`; request bodies still identify only a configured `deviceId`, not an override profile.

FFD 1.2 receipt items use flat `addReceiptItemUnmarked` parameters (`0x96`); shift completion returns `warningFlags`, and receipt/correction finalization uses the supplement's strict formats. Old base fiscal commands superseded by the supplement are unavailable in this profile. [docs/FFD12.md](docs/FFD12.md) contains the audit matrix, exact differences, supported subset, and remaining ambiguities. Existing examples retain the base profile; `examples/ffd12-*.json` require the FFD example configuration.

Version 0.7.3 adds FFD-only OISM and marking-key-update host settings (`0x82`–`0x85`), FN-format (`0x0F`), OISM-notification (`0x9E`), FN-memory (`0x9D`), and opaque service-parameter (`0xA2`) queries. The six getters perform fresh reads without request IDs or journal records. Both setters require complete parameters and a request ID. [The API guide](docs/API.md#ffd-12-host-settings-and-diagnostics) lists their fields; [examples/README.md](examples/README.md#ffd-12-examples) links to individual requests.

Version 0.7.4 adds FFD FN closure as one complete `executeOperation`: `0x14`, optional `0x17`, then `0x28`. Within the operation, numeric `0x28` finalizes the report selected by its preceding start: FN closure after `0x14`, or the existing settlement report after `0x27`. Their strict response layouts remain separate. [The API guide](docs/API.md#ffd-12-fn-closure-and-report-finalization) explains the named commands and failure behavior; [the FN-closure example](examples/ffd12-close-fiscal-storage.json) shows the full request.

Version 0.7.6 adds standalone FFD-only `registerKkt` with INN checksum validation, registration-number CRC16 validation using the connected KKT factory serial, and sender-email validation. Registration and re-registration share one flat parameter object and one request ID; re-registration reasons use a four-byte UINT32 LE mask, omitted for initial registration. [The API guide](docs/API.md#register-or-reregister-a-kkt) documents the required and optional fields. No new external dependencies are required.

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

Version 0.7.1 also accepts the 22-byte `getStatus` response captured from firmware `204`, preserving its undocumented trailing byte as optional `statusExtensionHex`. The documented 21-byte response retains its existing JSON shape. See [the API guide](docs/API.md#device-settings-and-cash-counters) for the compatibility details.

## Synchronize the device clock

Submit `cmd: "syncDateTime"`, an empty `params` object, and a required request ID; [examples/sync-clock.json](examples/sync-clock.json) provides the request. The command samples the Go server's local time when execution begins after queue waiting and any configured identity verification, drops seconds, and writes the normal `setDateTime` (`0x72`, TLV tag `30000`) value. A successful response contains `data.dateTime` in `YYYY-MM-DDTHH:mm` form. Serial and TCP devices use the same request.

Set the server's clock and process timezone correctly. For example:

```bash
TZ=Asia/Yekaterinburg ./bin/goterminalfa -config ./config.json
```

The sampled time comes from that process, not from the HTTP caller or browser. Repeating the same request ID replays the previous result and does not synchronize again; use a new request ID for each intended synchronization. No schedule or automatic synchronization is created.

## Stateful operations

Each state-changing request needs a stable `requestId`. Send a complete multi-command workflow with `cmd: "executeOperation"`; commands such as `beginReceipt`, `addReceiptItem`, and `finalizeReceipt` cannot be sent as independent HTTP calls. The whole sequence is validated before communication and runs without another request interleaving on that device.

To close an open shift from Pronogti, submit **`closeShiftIfOpen`** with the cashier and one request ID. Both protocol profiles return `alreadyClosed` and `shiftNumber`. An already closed shift completes after the status check. See the [API contract](docs/API.md#close-the-shift-if-it-is-open) and [example request](examples/close-shift-if-open.json).

For an FFD 1.2 unmarked receipt with a conditional shift check, use **`createReceipt`** and [examples/ffd12-create-receipt-auto-shift.json](examples/ffd12-create-receipt-auto-shift.json). It reads shift state, opens a shift with cashier data only if closed, then sends the items, any supplied receipt/payment data, and finalization. It leaves the shift open. You supply the amounts, payment fields, and VAT choices; none are inferred. Repeating its request ID replays the saved result and never prints a second receipt.

Version 0.7.5 adds **`createCorrectionReceipt`** with the same conditional shift opening and cleanup policy. It requires `additionalData.correctedCalculationDate` as direct UnixTime tag `1178`, then uses correction opening/finalization `0x25`/`0x26`. Its payment object remains optional and explicit. [The correction example](examples/ffd12-create-correction-receipt-auto-shift.json) provides a complete request; [the API contract](docs/API.md#create-a-correction-receipt-with-conditional-shift-opening) explains its fields and results. The existing static correction example remains available.

`executeOperation` is a static sequence; it does not branch on shift state or run error cleanup. `createReceipt` and `createCorrectionReceipt` are separate FFD-only commands and cannot be nested inside it. Their failure responses report the failed stage and any cleanup. Cancellation is attempted only after a report or receipt was confirmed opened by that request. A lost `0x24` or `0x26` finalization response stays unknown even if cancellation is acknowledged; cleanup is not proof that a completed receipt was rolled back. See [the shared cleanup contract](docs/API.md#failure-details-and-cleanup).

Allow sufficient client and HTTP request time for all primary work, the additional detached cleanup budget of up to 25 seconds, and result persistence. With a 60-second primary budget, plan for roughly 90 seconds plus margin; the FFD example uses a 95-second shutdown timeout. Increase older explicit shutdown values when using this workflow. A client disconnect does not erase the journaled outcome; use the request lookup endpoint if no final HTTP response arrives.

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

For the complete Ubuntu Server 22.04 deployment procedure, including USB discovery, stable device naming, configuration, systemd and troubleshooting, see [doc/DEPLOYMENT_UBUNTU_22.md](doc/DEPLOYMENT_UBUNTU_22.md).

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

In the base profile, four unavailable commands (`0x14`, `0x15`, `0x17`, `0x34`) return HTTP `501`. FFD 1.2 implements FN closure through `0x14`/`0x17`/`0x28`, while legacy `0x15` remains unavailable. The FFD profile also blocks superseded base fiscal formats and unimplemented marking workflows. Profile selection does not register or activate the device. The driver does not automatically retry device commands or fail over between transports.
