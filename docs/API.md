# HTTP API

The API accepts and returns UTF-8 JSON. Device protocol encoding and per-device serial/TCP connections are handled by the server. The same HTTP requests work with either transport. [COMMANDS.md](COMMANDS.md) lists the supported base-protocol scope; [commands.json](commands.json) contains the exported definitions. [LAN.md](LAN.md) explains configuring the device's raw TCP interface.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/v1/commands` | Execute a standalone command or complete operation |
| `GET` | `/api/v1/commands` | Inspect the default base catalogue, including unavailable entries |
| `GET` | `/api/v1/commands?deviceId=...` | Inspect the selected device's protocol-profile catalogue |
| `GET` | `/api/v1/devices` | List configured device identities and transport types |
| `GET` | `/api/v1/requests?deviceId=...&requestId=...` | Look up a persisted state-changing request |
| `GET` | `/healthz` | Check process/database health |

`/healthz` pings SQLite and returns `{"status":"ok"}` on success. It does not open serial ports, dial TCP devices, or establish KKT readiness. An unavailable journal produces HTTP `503` with `storage_error`.

When configured, `http.bearerToken` requires `Authorization: Bearer YOUR_TOKEN` for `/api/v1/*` requests. `/healthz` is exempt.

## Per-device protocol profiles

Each configured device selects `protocolProfile: "base"` or `"ffd1.2"`; omission means `base`. This setting selects command schemas and wire layouts independently of `connection.type`. Requests continue to use `deviceId`, `cmd`, `params`, and `requestId`; callers cannot override a configured profile in the request body.

`GET /api/v1/commands?deviceId=cashdesk-ffd12` returns `commands`, `deviceId`, and `protocolProfile` for that configured device. A missing, repeated, or malformed supplied ID is rejected, and an unknown device returns `404 unknown_device`. Bare `GET /api/v1/commands` preserves the base view. `GET /api/v1/devices` includes each selected profile.

Base command names remain stable, but profile-specific schemas can differ for the same name/code. Query the actual device catalogue before constructing fiscal requests. [FFD12.md](FFD12.md) describes the implemented supplement subset, the 13 audited overlapping codes, added commands, inherited common commands, and unavailable workflows.

## Request and response

| Field | Type | Meaning |
| --- | --- | --- |
| `deviceId` | String | Configured logical device ID |
| `cmd` | String | Canonical command name or registered alias |
| `params` | Object | Named command parameters; omission means an empty object |
| `requestId` | String | Required for state-changing commands and `executeOperation`; optional for reads |

IDs must match `[A-Za-z0-9][A-Za-z0-9_.-]{0,127}`: 1–128 ASCII characters, starting with a letter or digit. Unknown fields, duplicate JSON keys, and nesting beyond 64 levels are rejected. Send one JSON object with `Content-Type: application/json`; body size is limited by configuration.

Command names and declared aliases are case-sensitive. Numeric aliases use the documented byte, such as `0x03`; hexadecimal letter digits are accepted in upper or lower case, such as `0x2B` and `0x2b`. `ID` remains an alias for `getSerialNumber`. Responses identify the canonical command.

```json
{
	"deviceId": "cashdesk-1",
	"cmd": "getSerialNumber",
	"params": {},
	"requestId": "diagnostic-001"
}
```

```json
{
	"deviceId": "cashdesk-1",
	"cmd": "getSerialNumber",
	"requestId": "diagnostic-001",
	"status": "completed",
	"data": {
		"serialNumber": "550101046182"
	}
}
```

The factory serial is a string, preserving leading zeros. Read-only requests perform fresh queries and bypass the journal, even with a request ID. Their result lookup returns `404`.

## Parameter representation

The catalogue separates JSON types from their binary representation. It includes field order or TLV tags, nesting, repetition, requiredness, enum values, bitmasks, encoded lengths, and command-specific conditions.

- Supply `FVLN` quantities as exact decimal strings, for example `"1.25"`, never floating-point JSON numbers.
- Supply kopeck amounts as non-negative integer values. `VLN` accepts an exact JSON integer or decimal integer string, such as `12345` or `"12345"`. Fixed-width integer fields follow their declared widths; check the schema rather than assuming every numeric field accepts strings.
- Supply device-local `DATETIME5` values as `YYYY-MM-DDTHH:MM`, without an offset or timezone conversion. Date-only values use `YYYY-MM-DD`. A documented zero date can decode as `null`.
- Text is converted from JSON UTF-8 to CP866. Unrepresentable characters and encoded-length violations are rejected; padding is applied only where declared.
- Omission is preserved. Do not substitute zero for an optional field merely because it is absent. For example, omitted `suppressPrint` means the KKT's documented printing default.
- Unknown input fields are rejected. Unknown output TLV tags and repeated tags are preserved where the command exposes archive TLV data.

Use `GET /api/v1/commands` for the exact schema of each command rather than constructing binary fields yourself.

## Device settings and cash counters

All base-protocol settings pairs are implemented: `setNetworkSettings`/`getNetworkSettings`, `setOfdSettings`/`getOfdSettings`, `setPrinterSettings`/`getPrinterSettings`, and `setControlSettings`/`getControlSettings`. Getters return named TLV values, preserve unknown tags, and bypass the journal when invoked directly. Writes require request IDs.

Settings writes are **complete documented forms, not patches**:

| Setter | Required fields | Optional fields and behavior |
| --- | --- | --- |
| `setNetworkSettings` (`0x74`) | `dynamicAddress`, `ipAddress`, `subnetMask`, `gateway` | None. All address fields remain required when `dynamicAddress` is `1`; `0.0.0.0` is accepted as a placeholder |
| `setOfdSettings` (`0x76`) | `domain`, `tcpPort`, `timerSeconds` | Omit `ipAddress` to leave the fallback IP absent; an empty string or `null` is not omission |
| `setPrinterSettings` (`0x78`) | `fontSize`, `retract`, `additionalText` | `printerModel` is optional; an explicitly empty `additionalText` encodes an empty text setting |
| `setControlSettings` (`0x80`) | `controlInterface`, `tcpPort` | None. `0` selects serial and `1` selects Ethernet |

IPv4 addresses use dotted-decimal strings; subnet masks must be contiguous. Ports and the OFD timer retain their declared unsigned 16-bit ranges; the driver invents no defaults. Printer models `0`–`10` follow Appendix 4, page 36, whose explicit dictionary extends the settings table's older `0`–`7` wording.

Changing KKT network or control settings does not rewrite the server's configuration. If `controlInterface` is `1`, serial control may become unavailable; this change must be the final step in `executeOperation`. For a device configured with TCP, **any** `setNetworkSettings` or `setControlSettings` step must be last, including a write that leaves Ethernet selected but changes its address or command port. Standalone writes remain available with request IDs. Configure the server's corresponding connection separately when migrating. A lost response still requires outcome reconciliation. The example [set-control-settings.json](../examples/set-control-settings.json) deliberately selects serial mode `0`.

`getStatus` reads KKT state; `getPrintLineWidth` reads the configured printed-line width. `getCashCounters` returns exact integer `cashKopecks` and `electronicKopecks` values since shift opening. `resetCashCounters`, `printCashCountersReport`, `depositCash`, and `withdrawCash` are state-changing and require request IDs. Deposit and withdrawal each take `amountKopecks` as an unsigned integer or decimal-digit string and encode it as VLN. Runtime prerequisites such as an open shift and sufficient available cash remain device checks.

[read-settings.json](../examples/read-settings.json) combines the four getters in one `executeOperation`. It requires a request ID and is journaled as an application operation; repeating that ID replays its snapshot. Invoke individual getters directly when fresh, unjournaled reads are desired.

## Standalone state-changing commands

Commands such as `setDateTime`, `printText`, or `cancelDocument` can be submitted directly with a request ID. For example, [../examples/set-clock.json](../examples/set-clock.json) contains a clock-setting template. Its response normally contains `data: {}`.

These commands are journaled even when they do not create a fiscal document. Printer actions have observable effects. Archive commands `0x35` and `0x36` change a shared device cursor and are state-changing as well.

## Synchronize the clock from server time

`syncDateTime` is an application command that uses the Go server's local wall clock instead of a caller-supplied timestamp. It accepts only empty parameters and requires a request ID:

```json
{
	"deviceId": "cashdesk-1",
	"cmd": "syncDateTime",
	"requestId": "clock-sync-001",
	"params": {}
}
```

The server samples its local clock during execution, after the device queue and any configured expected-identity check. Sampling does not occur during request prevalidation. It formats calendar time to minute precision, dropping seconds, and sends the ordinary `setDateTime` command `0x72` with DATETIME5 TLV tag `30000`. On success, the response includes the exact value sent:

```json
{
	"deviceId": "cashdesk-1",
	"cmd": "syncDateTime",
	"requestId": "clock-sync-001",
	"status": "completed",
	"data": {
		"dateTime": "2026-11-01T10:30"
	}
}
```

The timestamp above is illustrative. The Go process's local timezone controls the calendar value; neither the HTTP caller's timezone nor a browser setting is used. Set the host clock correctly and, when desired, launch the process with an explicit `TZ`, for example `TZ=Asia/Yekaterinburg`. For systemd, set `Environment=TZ=Asia/Yekaterinburg` in the service's `[Service]` section, reload unit configuration with `systemctl daemon-reload`, and restart the service. The application does not change the server's clock or timezone.

Standard DATETIME5 validation and KKT clock-setting restrictions still apply. The binary year is an offset from 2000 and supports 2000–2255; the command does not bypass device state checks.

This is a journaled mutation on either transport. A matching completed request ID replays its original timestamp without sampling or writing the clock again. Use a new request ID for each new intended synchronization, and reconcile unknown outcomes before further action. No recurring or startup synchronization is scheduled.

## Complete operations

Use `executeOperation` for multi-step stateful workflows. Its command byte is empty because it is a driver operation, not a new KKT protocol command.

```json
{
	"deviceId": "cashdesk-1",
	"cmd": "executeOperation",
	"requestId": "open-shift-001",
	"params": {
		"commands": [
			{
				"cmd": "startOpenShift",
				"params": {
					"suppressPrint": 1
				}
			},
			{
				"cmd": "completeOpenShift",
				"params": {}
			}
		]
	}
}
```

This automatic-KKT example omits cashier details. Include `sendCashierData` between the two steps when required by the device's registered mode. The sequence creates a fiscal shift-opening report; it is not a read-only connectivity check.

An operation contains 1–256 command objects. Each has `cmd` and optional `params`. Nested `executeOperation` calls are rejected. All steps and their workflow structure are validated before device communication. Inner aliases are converted to canonical names before fingerprinting.

The device gate and SQLite request claim cover the complete sequence. Serial keeps one physical port open for the operation. TCP follows the KKT protocol's connect/send/receive/close lifecycle for each command, while preserving the operation's exclusive device gate across connections. Results are returned in order under `data.results`; each entry has its canonical `cmd` and decoded `data`. The outer `requestId` identifies the entire operation, not its individual steps.

The following workflow commands require this wrapper; a standalone request returns `400` with `operation_required`:

| Workflow | Required shape |
| --- | --- |
| Registration | `startRegistration`, registration data, `completeRegistration` |
| Open/close shift | Corresponding start, optional cashier data, corresponding completion |
| Receipt | `beginReceipt`, item/payment data, `finalizeReceipt` |
| Correction | `beginCorrectionReceipt`, correction data, `finalizeCorrectionReceipt` |
| Settlement report | `beginSettlementReport`, `finalizeSettlementReport` |
| Archive cursor | `selectFiscalDocumentTLV`, enough `readFiscalDocumentTLV` reads to consume the document |
| Formatted printer buffer | One or more `bufferFormattedText` calls, consumed by `printTextBuffer` or by `finalizeReceipt` in the same operation |

The catalogue's `operationRequired` flag identifies individual restricted commands. Workflows must finish in the same request; document workflows can explicitly use `cancelDocument` instead of finalizing. A later device error does not roll back earlier steps. If state-changing work has already succeeded, a subsequent failure results in `unknown` and blocks automatic repetition.

## Archive reads

`findFiscalDocument` (`0x30`) returns `documentType`, `ofdAcknowledged`, and opaque hexadecimal `documentData`. The driver does not guess a type-specific layout or equate that type byte with the separate TLV document-form codes.

`readFiscalDocumentTags` and `readRegistrationReportTags` return ordered `tlv` entries with `tag` and `valueHex`, plus the exact `rawHex`. Unknown and repeated tags remain available; nested values retain their payload bytes.

For a document read through the `0x35`/`0x36` cursor, use one complete operation. Set `repeatUntilComplete: true` on its `readFiscalDocumentTLV` step to read until the document length advertised by the selection is consumed. The flag is valid only for `0x36`.

The repeated step returns `data.chunks`, containing each decoded `0x36` result, and `data.byteCount`. Reads are bounded to 2048 commands and the selected document length, at most 65535 bytes. Empty progress, extra bytes, or an incomplete selected document fail the operation. See [../examples/read-document-tlv.json](../examples/read-document-tlv.json).

## Base receipt compatibility

- `addReceiptItem` encodes an `item` object as STLV `1059`; price is integer kopecks and quantity is a decimal string.
- VAT enum values `1`–`12` include the supplied base protocol's appendix-10 additions. New correction VAT-base fields map to tags `40007`–`40012`.
- `finalizeReceipt` accepts documented 10-, 15-, and 17-byte success layouts. `dateTime` and `shiftNumber` appear when supplied by the device firmware.
- `finalizeCorrectionReceipt` defaults to the configuration `1.0.18+` layout, omitting the legacy VAT-rate byte. Only supply `legacyVatRate` when deliberately targeting older firmware.
- Registration-dependent addresses, device details, taxation systems, agent information, and internet-payment contact requirements remain the caller's responsibility. The catalogue states the documented conditions; it does not infer a device's registration from missing fields.

The receipt example is a complete sequence with illustrative registration-dependent values. Adjust those values before deliberately submitting it to a registered device.

## FFD 1.2 receipts and shifts

For an `ffd1.2` device, `beginReceipt`/`beginCorrectionReceipt` accept optional `printSuppressionCode: 41029`, a two-byte value rather than the shifts' `suppressPrint` byte. Items use **`addReceiptItemUnmarked` (`0x96`) with flat named parameters**, not the base command's `item` wrapper. `paymentObject` is required; omitted `measure` is left absent so the device applies its documented pieces/units default.

`setReceiptPayment` (`0x2D`) contains only `taxSystem` and the five explicit kopeck amounts in this profile and can be used for either normal or correction receipts. Customer/cashier/settlement extras use `setReceiptAdditionalData` (`0x98`). A correction requires its direct `correctedCalculationDate` UnixTime field (tag `1178`) through `0x98` before finalization; no relative order with payment is imposed by the supplement. Optional `prescriptionNumber` is direct tag `1179` and applies only to authority-ordered correction. The legacy correction-basis STLV is not used.

Both finalizers take exactly `calculationSign` and `totalKopecks` with a fixed five-byte UINT40 amount. They require a 17-byte response containing `receiptNumber`, `documentNumber`, `fiscalSign`, `dateTime`, and `shiftNumber`; `additionalText`, `legacyVatRate`, and shorter base response variants do not apply.

FFD shifts keep `startOpenShift`/`startCloseShift` optional `suppressPrint` behavior. `completeOpenShift`/`completeCloseShift` require 11 bytes and return `warningFlags` in addition to the three base identifiers. Optional `sendShiftOpeningData` (`0xE4`), `sendShiftClosingData` (`0xE5`), and `sendSettlementReportData` (`0xE6`) remain within their complete operation. The inherited `sendCashierData` (`0x2F`) is retained, but the supplement documents E4/E5 as its dedicated shift-data paths.

The `ffd12-*` examples target `cashdesk-ffd12` and show complete shift, unmarked receipt, correction, cancellation, and settlement-report operations. They are deliberate mutation templates, not automatically executed tests against physical hardware. Settings and clock reads retain their read-only policy; `0x72`, `0x74`, `0x76`, and `0x80` are still writes requiring request IDs.

## Idempotency and recovery

Every implemented state-changing command requires a request ID. `executeOperation` also requires one, even if its chosen steps are only reads.

1. Validation and command-name normalization occur before communication.
2. SQLite atomically claims `(deviceId, requestId)` and stores the canonical command, parameters, and fingerprint.
3. A completed matching request replays its original HTTP status and response body with `Idempotency-Replayed: true`.
4. Reusing that identity with different parameters returns `409 idempotency_conflict`.
5. A matching request already running returns `409 request_in_progress` with `status: in_progress`.
6. Uncertain outcomes return `409 outcome_unknown` with `status: unknown`; they are never executed again automatically.

Failures known to occur before the mutating handler starts, such as failure to open the port or verify the expected device identity, are definite failures. TCP dial failures and failures known to occur before any command bytes were written are also definite unsent failures. Partial delivery, lost responses, or a later batch failure after earlier state-changing work can leave an unknown outcome. A client disconnect does not undo a command already sent.

After restart, outstanding `in_progress` entries become `unknown`. The lookup endpoint returns completed original responses, existing in-progress/unknown states as `409`, or `404` for absent records. An unavailable journal fails before starting a newly claimed command.

Keep logical device IDs assigned to the same physical KKT. Request deduplication is not proof that an uncertain fiscal document was never created: inspect and reconcile the device's state/archive before deciding how to proceed. Do not bypass an unknown result by changing the request ID.

The non-default protocol profile is included in the request fingerprint. Changing an existing device from `base` to `ffd1.2` cannot silently replay a base mutation for the new wire format: the same request ID conflicts. Lookup still retrieves the earlier persisted result. Keep old unresolved outcomes for reconciliation rather than bypassing them with a profile change.

## Error responses

Errors contain a machine `error.code`, a readable `error.message`, and, for a KKT protocol rejection, its numeric `error.deviceCode`. Parsed request identity is retained where available.

| HTTP status | Error codes |
| --- | --- |
| `400` | `invalid_request`, `operation_required` |
| `401` | `unauthorized` |
| `404` | `unknown_device`, `unknown_command`, `not_found` |
| `405` | `method_not_allowed` |
| `409` | `idempotency_conflict`, `request_in_progress`, `outcome_unknown` |
| `413` | `invalid_request` for an oversized body |
| `415` | `invalid_request` for an unsupported content type |
| `500` | `internal_error` |
| `501` | `command_unavailable` |
| `502` | `device_error`, `protocol_error`, `transport_error`, `device_identity_mismatch` |
| `503` | `queue_full`, `storage_error` |
| `504` | `device_timeout` |

`completed` means a successful decoded result; `failed` means a definite failure; `in_progress` and `unknown` describe journaled execution states. A full waiting queue or an expired waiting deadline starts no exchange. TCP dialing is bounded by `connectTimeout` and its whole connect/write/read exchange by `responseTimeout`, within the HTTP request deadline; cancellation interrupts network reads and writes. Serial cancellation interrupts response reads but cannot forcibly interrupt synchronous serial opening, configuration, or writes. Neither transport automatically retries commands or switches to the other transport after an error.
