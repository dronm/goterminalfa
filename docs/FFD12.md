# FFD 1.2 protocol profile

`goterminalfa` supports an explicit **`ffd1.2`** per-device profile for the implemented parts of **«Протокол информационного обмена ККТ Терминал-ФА — дополнение для работы с ФФД версии 1.2»**. Page references below refer to that supplied supplement unless marked as base.

The supplement is not a complete replacement for the base protocol. It redefines some existing command bytes, adds new commands, and leaves common commands unspecified. The driver selects audited definitions explicitly rather than guessing from a response length or silently combining incompatible fiscal payloads. The selected catalogue contains **80 entries**: **65 implemented wire commands**, **12 explicitly unavailable entries**, and three application commands. The FFD-only `createReceipt` adds a conditional shift-opening and receipt workflow; it is not a new wire command. [commands-ffd12.json](commands-ffd12.json) exports those definitions.

## Select a profile and inspect its catalogue

The default is `base`; omitting `protocolProfile` preserves the existing base wire formats and API. Select FFD 1.2 in a device entry:

```json
{
	"id": "cashdesk-ffd12",
	"protocolProfile": "ffd1.2",
	"expectedSerialNumber": "550101046182",
	"queueSize": 32,
	"connection": {
		"type": "serial",
		"port": "/dev/terminalfa",
		"baudRate": 115200,
		"responseTimeout": "25s"
	}
}
```

[../config.ffd12.example.json](../config.ffd12.example.json) supplies a complete configuration. Adjust the device path/identity and verify the device's actual fiscal format before selecting the profile. Profile selection is independent of serial/TCP transport and does not register, activate, or upgrade the KKT/FN.

Discover the selected definitions through the normal HTTP API:

```bash
curl --fail-with-body --silent --show-error \
	'http://127.0.0.1:8080/api/v1/commands?deviceId=cashdesk-ffd12'
```

Add a configured bearer header if needed. The response contains `deviceId`, `protocolProfile`, and `commands`. Device listing also identifies the selected profile. Bare `GET /api/v1/commands` and [commands.json](commands.json) retain the base catalogue; [commands-ffd12.json](commands-ffd12.json) is the FFD-specific export. Profile selection is not an extra field accepted in command requests.

One logical device still owns one execution gate and its normal idempotency journal. Do not configure the same physical KKT twice to obtain two profiles. Changing an existing device's profile affects its non-base request fingerprint: reuse of an old base mutation ID conflicts instead of replaying a different wire definition. Old results remain accessible for reconciliation.

## Audit of the 13 overlapping command bytes

| Code | Canonical command | Supplement page | FFD 1.2 decision |
| --- | --- | --- | --- |
| `0x19` | `addCashlessPaymentDetails` | 27 | Same documented TLV shape; allowed only for normal receipts and after payment `0x2D` |
| `0x20` | `getShiftParameters` | 9 | Same five-byte response: shift state, shift number, receipt number |
| `0x21` | `startOpenShift` | 9 | Same optional `suppressPrint` BYTE; omission prints; opening can spend about 15 seconds updating marking-verification keys |
| `0x22` | `completeOpenShift` | 10 | Strict 11-byte response: three identifiers plus `warningFlags` |
| `0x23` | `beginReceipt` | 22 | Optional `printSuppressionCode` UINT16 LE; `41029` suppresses the printed form |
| `0x24` | `finalizeReceipt` | 27–28 | Calculation sign plus fixed five-byte UINT40 total; strict 17-byte response; no base `additionalText` argument |
| `0x25` | `beginCorrectionReceipt` | 22 | Same optional UINT16 printing parameter as `0x23` |
| `0x26` | `finalizeCorrectionReceipt` | 28 | Calculation sign plus UINT40 total, strict 17-byte response; no base VLN/legacy VAT encoding |
| `0x27` | `beginSettlementReport` | 12 | Base request/response layout retained with supplement provenance |
| `0x28` | `finalizeSettlementReport` | 12 | Base settlement-report layout retained; not repurposed as the conflicting closure finalizer on page 13 |
| `0x29` | `startCloseShift` | 10 | Same optional `suppressPrint` BYTE and printing default |
| `0x2A` | `completeCloseShift` | 11 | Strict 11-byte response, including `warningFlags` |
| `0x2D` | `setReceiptPayment` | 27 | Only tax system and five explicit payment amounts; valid for normal and correction receipts; extras move to `0x98` |

A ten-byte base shift result does not become an FFD result by inventing a warning byte. Similarly, a shorter base receipt result does not satisfy the FFD 17-byte layout. The configured profile determines the schema before an exchange occurs.

## Implemented supplement additions

| Code | Canonical command | Page | Schema and execution rule |
| --- | --- | --- | --- |
| `0x96` | `addReceiptItemUnmarked` | 22–23 | Flat item TLVs for an ordinary unmarked item; normal or correction receipt operation |
| `0x98` | `setReceiptAdditionalData` | 26–27 | Receipt/customer/cashier/settlement extras and direct correction fields; normal or correction receipt operation |
| `0xE4` | `sendShiftOpeningData` | 9 | Optional opening-data command between `0x21` and `0x22` |
| `0xE5` | `sendShiftClosingData` | 10–11 | Optional closing-data command between `0x29` and `0x2A` |
| `0xE6` | `sendSettlementReportData` | 12 | Optional report-data command between `0x27` and `0x28` |

All five commands require a complete operation; independent HTTP calls return `400 operation_required`. Use a static `executeOperation` sequence or, for its supported normal receipt path, the FFD-only `createReceipt` workflow. Every state-changing operation requires one request ID. Validation occurs before device communication, and the whole sequence retains the device gate and existing transport lifecycle.

### Unmarked receipt items

`addReceiptItemUnmarked` takes these fields directly in `params`:

```json
{
	"name": "Example item",
	"priceKopecks": 10000,
	"quantity": "1",
	"vatRate": 6,
	"paymentMethod": 4,
	"paymentObject": 1
}
```

Do not wrap them in the base `item` object or STLV `1059`. `paymentObject` is required. `quantity` is an exact decimal string and price is integer kopecks. Optional `measure` maps to tag `2108`; omission remains absent and the KKT applies its documented default `0` for pieces/units.

The schema also supports the documented optional item fields, per-item agent data, and repeated industry attributes. For a nonzero agent role, supplier INN is explicitly required by the supplement. Starred optional supplier/agent fields are not made mandatory merely because they belong to a role. Industry entries use STLV `1260`; each supplied object requires its four documented children. Use the live catalogue for exact names, enums, presence conditions, and encoded lengths.

Appendix 5 on page 42 includes VAT values 1–12 and date-related transition notes. The driver encodes the selected value; it does not infer tax applicability from the Go server clock or convert an unmarked-item command into a marked-goods workflow.

### Additional receipt and correction data

`setReceiptAdditionalData` supplies top-level TLVs. Normal receipts use it when required details must be supplied, such as customer contact or settlement address/place. It is not automatically mandatory for every normal receipt.

Correction receipts must include `correctedCalculationDate`, direct UnixTime tag `1178`, through `0x98` before `finalizeCorrectionReceipt`. `prescriptionNumber` is direct tag `1179`, up to 32 CP866 bytes, and is included only for an authority-ordered correction. No request field here identifies whether a prescription legally applies; the caller supplies it when appropriate.

The corrected calculation date must not follow the KKT correction-document formation date; the caller must select the correct date. There is no legacy `1173` correction type or `1174`/`1177` correction-basis STLV in this command. The supplement groups `0x98` and `0x2D` in its flow but does not impose their relative order; the operation validator requires the correction date by finalization. `0x19` is separately and explicitly restricted to normal receipts after `0x2D`.

Receipt-level industry attributes use repeated STLV `1261`. Their child dates and customer birth dates use the supplement's `DD.MM.YYYY` text form, unlike device DATETIME values. Metadata records the documented customer/authority dictionaries and right-padding rules.

### Shift/report data

If E4 or E5 is supplied, provide `cashierName`, `documentAddress`, and `documentPlace`; `cashierInn` is optional and must contain twelve digits when present. `reportAttribute` and `reportDataHex` are optional as a pair. The attribute is at most 32 CP866 bytes and the data at most 32 decoded hexadecimal bytes. Their tags are `1276`/`1277` for opening and `1278`/`1279` for closing.

E6 uses `documentAddress`, `documentPlace`, and the optional paired `reportAttribute`/`reportDataHex` fields, with tags `1280`/`1281` for the pair.

E4/E5/E6 are optional commands, not unconditional extra steps. The supplied shift examples also include inherited `sendCashierData` (`0x2F`) as requested; the supplement does not redefine that command, and its dedicated data paths are E4/E5. Sending both does not mean that both are mandatory in every operating mode.

## Superseded base fiscal formats

These registered base commands are explicitly unavailable under `ffd1.2` and return `501 command_unavailable`, rather than emitting legacy fiscal data:

| Base code(s) | Replacement or reason |
| --- | --- |
| `0x12`, `0x13`, `0x16` | The supplement defines different registration bytes E1/E2/E3; that registration workflow is not implemented |
| `0x1F`, `0x3F` | Settlement/device details belong to additional receipt data `0x98` |
| `0x2B`, `0x2C` | Ordinary items and their agent fields use `0x96`; additional receipt fields use `0x98` |
| `0x2E` | FFD correction data uses the supplement's item/payment/additional-data sequence, including direct correction date in `0x98` |

These restrictions do not remove or change the base profile's existing functionality.

## Unimplemented and unresolved supplement workflows

| Supplement code(s) | Source page(s) | Status and reason |
| --- | --- | --- |
| `0x82`, `0x83` | 4 | OISM host settings are outside this implemented subset |
| `0x84`, `0x85` | 4 | Marking-verification key-update host settings are not implemented |
| `0x0F`, `0x9E`, `0x9D` | 5–6 | FN-format/OISM-notification/free-memory queries are not implemented |
| `0xE1`, `0xE2`, `0xE3` | 7–8 | FFD registration is not implemented; page-8 reason BYTE and page-35 bitmask wording need an explicit resolved definition before extending it |
| `0x14`, `0x17`, closure `0x28` | 13 | Start/data layouts are present, but the closure finalizer conflicts with settlement `0x28` on page 12 and has a different response; complete closure remains unavailable |
| `0x90`, `0x91`, `0x92`, `0x94`, `0x95` | 14–21 | Marking-code verification/state workflows are not implemented |
| `0x97` | 24–25 | Marked-item submission is not implemented |
| `0xDD`, `0xA2` | 28 | FFD activation-code submission and service-parameter query are not implemented |

The base `0x15` closure finalizer is not guessed from the supplement's conflicting `0x28`. The inherited `0x34` remains unavailable because its layout is not documented. The catalogue states reasons for registered unavailable entries; supplement commands not registered in this subset return `404 unknown_command`.

This is an implementation of the selected FFD shifts and unmarked receipt workflows, not a claim of full FFD 1.2 or marked-goods coverage. Required device firmware, fiscal registration, FN state, and any manufacturer activation must already support the intended commands.

## Inherited common commands and side effects

Where the supplement does not redefine a common command, explicitly retained base definitions remain available. Examples include `cancelDocument` (`0x10`), `sendCashierData` (`0x2F`), device identification/status, clock access, and general network/OFD/printer/control settings. Each definition retains its own source metadata.

These are reads: `getDateTime` (`0x73`), `getNetworkSettings` (`0x75`), `getOfdSettings` (`0x77`), `getPrinterSettings` (`0x79`), and `getControlSettings` (`0x81`). Their setters `0x72`/`0x74`/`0x76`/`0x78`/`0x80` are **writes**, not status queries, and still require request IDs. The `syncDateTime` application command is likewise a journaled clock write using inherited `0x72`.

TCP/serial deadlines, settings final-step guards, and idempotency replay retain their existing behavior. Static batches do not branch or run automatic error cancellation. The dedicated `createReceipt` workflow checks shift state and conditionally opens it, then submits items, any supplied additional/payment data, and finalization. It leaves an opened shift open and never infers payment values.

On failure, `createReceipt` attempts cancellation only after a report/receipt was confirmed opened by that request. Cleanup uses a new session after the primary closes, a detached 25-second context, and a fresh factory-serial check to bind cancellation to the same physical KKT; the device gate stays held. A lost receipt finalization response remains unknown even if cancellation is acknowledged. [API.md](API.md#create-a-receipt-with-conditional-shift-opening) defines its outcome and response details. This workflow cannot be nested inside `executeOperation`.

## Examples

Use `config.ffd12.example.json` for these `cashdesk-ffd12` requests. Existing unprefixed examples remain base-profile examples.

| File | Purpose |
| --- | --- |
| `ffd12-read-shift.json` | Fresh read-only `0x20` state check |
| `ffd12-open-shift.json` | Complete `0x21` → `0x2F` → optional `0xE4` → `0x22` sequence |
| `ffd12-close-shift.json` | Complete `0x29` → `0x2F` → optional `0xE5` → `0x2A` sequence |
| `ffd12-create-receipt.json` | Complete `0x23` → `0x96` → optional `0x98` → `0x2D` → `0x24` unmarked receipt |
| `ffd12-create-receipt-auto-shift.json` | `createReceipt`: conditional shift opening followed by the receipt, with owned-document cleanup reporting |
| `ffd12-create-correction.json` | Complete correction with `0x25`, unmarked item, direct correction date in `0x98`, payment, and `0x26` |
| `ffd12-cancel-receipt.json` | Begin then cancel an unfinalized receipt with inherited `0x10` |
| `ffd12-settlement-report.json` | Complete `0x27` → optional `0xE6` → `0x28` report |
| `ffd12-read-clock.json` | Read-only clock query |
| `ffd12-sync-clock.json` | Deliberate server-clock synchronization write with a request ID |

All state-changing examples are templates for deliberate submission, not automatically run scripts. Replace illustrative addresses, cashier data, taxonomy/VAT choices, payment amounts, and correction dates with appropriate values for the registered device. Cancellation affects an unfinalized document; it does not undo a completed receipt.
