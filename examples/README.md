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
| `ffd12-read-shift.json` | Fresh read-only `getShiftParameters` (`0x20`) |
| `ffd12-open-shift.json` | Complete opening with inherited cashier `0x2F`, optional FFD `0xE4`, and the 11-byte final response |
| `ffd12-close-shift.json` | Complete closing with inherited cashier `0x2F` and optional `0xE5` |
| `ffd12-create-receipt.json` | Complete unmarked receipt with flat `0x96`, optional extras `0x98`, payment `0x2D`, and strict finalization `0x24` |
| `ffd12-create-receipt-auto-shift.json` | FFD-only `createReceipt` checks shift state, opens it when closed, and creates the receipt; returns cleanup details on failure |
| `ffd12-create-correction.json` | Complete correction with direct `correctedCalculationDate` in `0x98` and finalization `0x26` |
| `ffd12-cancel-receipt.json` | Begin and cancel an unfinalized receipt through inherited `0x10` |
| `ffd12-settlement-report.json` | Complete settlement report with optional `0xE6` data |
| `ffd12-read-clock.json` | Inherited read-only `0x73` |
| `ffd12-sync-clock.json` | Explicit journaled clock synchronization through inherited `0x72` |

FFD receipt items have no base `item` wrapper. The shift `suppressPrint` BYTE differs from the optional receipt `printSuppressionCode` UINT16 value `41029`. E4/E5/E6 are optional commands; if included, their documented fields are required and report attribute/data must be supplied as a pair. The examples include both `0x2F` and E4/E5 to demonstrate the retained and supplement paths, not to require both in every mode.

All fiscal examples remain deliberate mutation templates. Replace illustrative cashier/address, tax/payment, and correction-date values for the actual device. No marking-code verification or marked-item workflow is implemented. Settings reads remain reads; their setters, including `0x72`/`0x74`/`0x76`/`0x80`, still require request IDs under this profile.

`createReceipt` takes a required top-level `cashierName` for conditional shift opening, 1–200 flat `0x96` item objects, calculation sign, and total. Its optional `payment` is a complete `0x2D` object; no payment is synthesized when omitted. Optional `additionalData` is a `0x98` object. The cashier name is still required when the shift is already open. The auto-shift example omits both printing-suppression flags and preserves the device's normal printing defaults. It leaves the shift open and cannot be placed inside `executeOperation`.

Use the same request ID only to retrieve/replay that exact intended receipt; replay never prints another copy. If failure includes cleanup, a successful cancellation is not proof that an ambiguously finalized receipt did not exist. Reconcile `unknown` results before further action. Allow the full primary execution time plus the detached cleanup budget of up to 25 seconds in caller/shutdown planning.

Changing the control interface to Ethernet (`controlInterface: 1`) can disable the serial control path and must be the final step of an `executeOperation`. On a TCP-configured device, any network-settings or control-settings write must be the final step, even if its interface value remains unchanged. These writes do not modify the server's per-device configuration; change its connection separately. No supplied script selects Ethernet or sends mutating examples automatically. [../docs/LAN.md](../docs/LAN.md) describes the migration and read-only connection check.

For request identity, schema details, archive bounds, and uncertain outcomes, see [../docs/API.md](../docs/API.md). Full field definitions are available from `GET /api/v1/commands` and [../docs/commands.json](../docs/commands.json).
