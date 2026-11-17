# Request examples

Send read-only requests to `POST /api/v1/commands`:

```bash
curl --fail-with-body --silent --show-error \
	-H 'Content-Type: application/json' \
	--data-binary @examples/read-firmware.json \
	http://127.0.0.1:8080/api/v1/commands
```

Add the configured bearer header if needed. The same pattern applies to these read-only files:

Requests are identical for serial and TCP devices: `deviceId` selects the transport configured by the server. Use [../config.tcp.example.json](../config.tcp.example.json) for LAN or [../config.mixed.example.json](../config.mixed.example.json) for mixed units. The TCP example's `192.0.2.50:7778` is illustrative; the KKT's actual IP and command port must be configured separately.

Existing examples without the `ffd12-` prefix use the default base protocol. The profile is selected per configured device, independently of transport; query `GET /api/v1/commands?deviceId=...` for the exact schemas.

| File | Command |
| --- | --- |
| `read-serial-number.json` | `getSerialNumber` |
| `read-serial-number-alias.json` | Alias `ID`, with an optional tracing request ID |
| `read-firmware.json` | `getFirmwareVersion` |
| `read-clock.json` | `getDateTime` |
| `read-status.json` | `getStatus` |
| `read-cash-counters.json` | `getCashCounters` |

`read-settings.json` takes a snapshot with all four settings getters in one `executeOperation`. Although its inner commands only read, the application operation requires a request ID and is journaled. Reusing that ID replays the saved snapshot. Use a new ID for a new intentional snapshot, or call the individual getters for fresh reads without journal records.

The following files are **templates for deliberate state-changing operations**, not commands run by an example script:

| File | Effect and prerequisites |
| --- | --- |
| `set-clock.json` | Sets the device-local calendar time; replace the illustrative timestamp |
| `sync-clock.json` | Sets the KKT clock from the Go process's current local time, with seconds dropped; no timestamp parameter is accepted |
| `open-shift.json` | Opens a complete shift with printing suppressed; assumes automatic operation and fiscal mode |
| `create-receipt.json` | Creates a 100-ruble cash receipt, no VAT, general taxation; adjust all registration-dependent values and ensure the shift is open |
| `read-document-tlv.json` | Selects document 1 and drains its archive TLV cursor under one protected operation |
| `set-network-settings.json` | Selects DHCP and supplies all required IPv4 fields as `0.0.0.0` placeholders |
| `set-ofd-settings.json` | Sets the OFD domain, TCP port, and timer; replace the example domain and values with your provider's settings |
| `set-printer-settings.json` | Sets normal font, no retract, and explicitly empty additional text; leaves the optional model absent |
| `set-control-settings.json` | Selects serial control mode `0` and explicitly supplies the KKT TCP command port |
| `deposit-cash.json` | Records a 100-ruble cash deposit |
| `withdraw-cash.json` | Records a 10-ruble cash withdrawal; the device checks available cash and shift state |

Each template has one illustrative `requestId`. Use the same ID only when retrying or looking up that exact intended operation; a matching completed request replays the saved response. Do not replace an unknown operation's ID to force it to run again.

The receipt uses `quantity: "1"` as an exact decimal string and integer kopecks for amounts. Its complete sequence is `beginReceipt`, `addReceiptItem`, `setReceiptPayment`, and `finalizeReceipt`. The example does not infer the taxation system, VAT treatment, registered address, cashier, agent, or internet-payment requirements of your KKT.

The settings setters use full documented forms, not patches. Omission of the optional OFD fallback IP or printer model remains omission; required fields are not filled with inferred defaults. Port numbers and timer values in the templates are illustrative, not manufacturer defaults. Cash templates use integer strings to demonstrate exact VLN amounts.

`sync-clock.json` works with either transport. The server samples time after queue waiting and any configured identity verification, according to its own clock and timezone. For example, start it with `TZ=Asia/Yekaterinburg` when that is the intended device calendar timezone. A repeated request ID replays the original `data.dateTime` and does not set a later time; use a new ID for each new intended synchronization. The example creates no automatic or scheduled synchronization.

## FFD 1.2 examples

Load [../config.ffd12.example.json](../config.ffd12.example.json) and use its `cashdesk-ffd12` logical device for the `ffd12-*` files. These select the explicit FFD profile; the unprefixed examples above retain base formats. [../docs/FFD12.md](../docs/FFD12.md) provides the source audit and profile limitations, and [../docs/commands-ffd12.json](../docs/commands-ffd12.json) contains the selected schemas.

| File | Purpose |
| --- | --- |
| `ffd12-get-oism-settings.json` | Fresh read-only OISM host, port, and timeout query (`0x83`) |
| `ffd12-set-oism-settings.json` | Journaled complete OISM settings write (`0x82`); replace placeholder host and port |
| `ffd12-get-marking-key-update-settings.json` | Fresh read-only key-update host and port query (`0x85`) |
| `ffd12-set-marking-key-update-settings.json` | Journaled complete key-update settings write (`0x84`); replace placeholder host and port |
| `ffd12-get-fiscal-storage-format.json` | Fresh read-only active/supported FFD-format query (`0x0F`) |
| `ffd12-get-oism-notification-status.json` | Fresh read-only notification-state query (`0x9E`) |
| `ffd12-get-fiscal-storage-memory.json` | Fresh read-only FN-memory-resource query (`0x9D`) |
| `ffd12-get-service-parameters.json` | Fresh read-only opaque service-text query (`0xA2`) |
| `ffd12-apply-activation-code.json` | Apply the vendor's device-specific activation code through standalone, journaled `0xDD`; replace the illustrative code |
| `ffd12-register-kkt.json` | Initial registration through `registerKkt`; replace the illustrative identity and registration values |
| `ffd12-reregister-kkt.json` | Re-registration without FN replacement, illustrating reason bit 31 as `2147483648` |
| `ffd12-read-shift.json` | Fresh read-only `getShiftParameters` (`0x20`) |
| `ffd12-open-shift.json` | Complete opening with inherited cashier `0x2F`, optional FFD `0xE4`, and the 11-byte final response |
| `ffd12-close-shift.json` | Complete closing with inherited cashier `0x2F` and optional `0xE5` |
| `ffd12-create-receipt.json` | Complete unmarked receipt with flat `0x96`, optional extras `0x98`, payment `0x2D`, and strict finalization `0x24` |
| `ffd12-create-receipt-auto-shift.json` | FFD-only `createReceipt` checks shift state, opens it when closed, and creates the receipt; returns cleanup details on failure |
| `ffd12-create-correction.json` | Complete correction with direct `correctedCalculationDate` in `0x98` and finalization `0x26` |
| `ffd12-create-correction-receipt-auto-shift.json` | FFD-only `createCorrectionReceipt` checks/opens the shift, requires the direct correction date, and reports confirmed-document cleanup |
| `ffd12-cancel-receipt.json` | Begin and cancel an unfinalized receipt through inherited `0x10` |
| `ffd12-settlement-report.json` | Complete settlement report with optional `0xE6` data |
| `ffd12-close-fiscal-storage.json` | Complete FN closure with numeric `0x14`, optional report data `0x17`, and context-selected `0x28` |
| `ffd12-read-clock.json` | Inherited read-only `0x73` |
| `ffd12-sync-clock.json` | Explicit journaled clock synchronization through inherited `0x72` |

Registration examples use a checksum-valid RN for INN `7707083893` and KKT factory serial `550101046182`. Replace these illustrative values with the actual issued values, names, OFD details, address, place, and taxation systems. For re-registration, select the report type and reason mask for the intended change. The driver reads the actual factory serial before E1, and a mismatching RN fails without starting registration. The examples call `registerKkt` directly, with one request ID for E1/E2/E3. [The API contract](../docs/API.md#register-or-reregister-a-kkt) lists all optional tags and mode constraints.

FFD receipt items have no base `item` wrapper. The shift `suppressPrint` BYTE differs from the optional receipt `printSuppressionCode` UINT16 value `41029`. E4/E5/E6 are optional commands; if included, their documented fields are required and report attribute/data must be supplied as a pair. The examples include both `0x2F` and E4/E5 to demonstrate the retained and supplement paths, not to require both in every mode.

The six new `ffd12-get-*` examples omit request IDs and query afresh without journal records. The two `ffd12-set-*` examples require complete parameters and a request ID. Hosts `oism.example.invalid` and `keys.example.invalid`, and port `443`, are placeholders; replace them with the settings for the actual KKT. The OISM timeout is explicitly `30` seconds, as recommended by the supplement, rather than an implicit driver default. Setters return `data: {}`. These host settings do not implement marking-code verification or marked-item receipts.

`ffd12-close-fiscal-storage.json` closes the FN through one complete `executeOperation` and one request ID. Replace its illustrative address and place before deliberate submission. The `0x17` step may be omitted; when present, its address/place are required, and any `reportAttribute`/`reportDataHex` values must be supplied as a pair. Numeric `0x28` finalizes FN closure after `0x14`, or settlement reporting after `0x27`. Explicit finalizer names must match their corresponding start. The closure result contains only `documentNumber` and `fiscalSign`; the settlement result additionally contains `unconfirmedCount` and `firstUnconfirmedDate`. Context is resolved within the request, without inspecting response length or carrying report state across requests.

These report batches stop on the first error and send no later steps. They do not roll back accepted commands or automatically cancel the report. A later failure after state-changing work leaves an unknown outcome that is not executed again automatically. The legacy FFD `0x15` finalizer remains unavailable; use the documented `0x28` operation path. [The API guide](../docs/API.md#ffd-12-fn-closure-and-report-finalization) shows a minimal named form.

All fiscal examples remain deliberate mutation templates. Replace illustrative cashier/address, tax/payment, and correction-date values for the actual device. No marking-code verification or marked-item workflow is implemented. Settings reads remain reads; their setters, including `0x72`/`0x74`/`0x76`/`0x80`, still require request IDs under this profile.

`ffd12-apply-activation-code.json` is a separate mutation template for `applyActivationCode`, available only in the `ffd1.2` profile. Obtain the activation code for the actual KKT from the vendor; the driver does not generate or retrieve it. Replace `activationCodeHex` with exactly 32 hexadecimal characters representing the vendor's 16-byte code, without spaces or a `0x` prefix. The example value is illustrative. Send this request directly with a `requestId`; no `executeOperation` wrapper is required. A successful response has `data: {}`. Hexadecimal letter case is normalized for request identity, so a retry with the same ID and different letter case replays the same operation.

`createReceipt` takes a required top-level `cashierName` for conditional shift opening, 1–200 flat `0x96` item objects, calculation sign, and total. Its optional `payment` is a complete `0x2D` object; no payment is synthesized when omitted. Optional `additionalData` is a `0x98` object. The cashier name is still required when the shift is already open. The auto-shift example omits both printing-suppression flags and preserves the device's normal printing defaults. It leaves the shift open and cannot be placed inside `executeOperation`.

`createCorrectionReceipt` uses the same fields and item bounds, with required `additionalData.correctedCalculationDate` (direct UnixTime tag `1178`). Its new auto-shift example supplies `1704067200` as an illustrative past timestamp; replace it with the date of the corrected calculation, no later than the KKT's correction-document date. Add `prescriptionNumber` only for an authority-ordered correction. The workflow reads shift state, opens a closed shift with `0x21`/`0x2F`/`0x22`, then runs `0x25`, one `0x96` per item, `0x98`, optional `0x2D`, and `0x26`. It leaves an opened shift open, requires a cashier name even for the already-open branch, and cannot be nested inside `executeOperation`. The example includes explicit payment; omitting it sends no `0x2D` and infers no amounts. The existing static `ffd12-create-correction.json` remains available.

Both dedicated receipt workflows use one request ID for primary work and recovery. Cancellation is eligible only for a document confirmed opened by that request. Recovery uses a fresh session, a detached 25-second deadline, and another factory-serial check while retaining the device gate. A lost `0x24` or `0x26` response remains unknown even if cancellation succeeds. Repeating the same request ID retrieves the recorded result without creating another receipt. See [the correction API contract](../docs/API.md#create-a-correction-receipt-with-conditional-shift-opening).

Use the same request ID only to retrieve/replay that exact intended receipt; replay never prints another copy. If failure includes cleanup, a successful cancellation is not proof that an ambiguously finalized receipt did not exist. Reconcile `unknown` results before further action. Allow the full primary execution time plus the detached cleanup budget of up to 25 seconds in caller/shutdown planning.

Changing the control interface to Ethernet (`controlInterface: 1`) can disable the serial control path and must be the final step of an `executeOperation`. On a TCP-configured device, any network-settings or control-settings write must be the final step, even if its interface value remains unchanged. These writes do not modify the server's per-device configuration; change its connection separately. No supplied script selects Ethernet or sends mutating examples automatically. [../docs/LAN.md](../docs/LAN.md) describes the migration and read-only connection check.

For request identity, schema details, archive bounds, and uncertain outcomes, see [../docs/API.md](../docs/API.md). Full field definitions are available from `GET /api/v1/commands` and [../docs/commands.json](../docs/commands.json).
