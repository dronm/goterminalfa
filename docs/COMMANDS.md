# Command index

The default **base** catalogue exposes **75 entries** across **68 implemented base-protocol commands**, **four unavailable base-protocol entries**, and three application commands (**`executeOperation`**, **`syncDateTime`**, and **`closeShiftIfOpen`**). All **72 command entries listed in the supplied base protocol** are represented. The table below describes that default profile.

Read-only commands perform fresh device queries and are not journaled. State-changing commands require a `requestId`. **Operation** means the command must appear inside a complete `executeOperation`; **Direct** allows a standalone request. Unavailable entries return `501 command_unavailable` without device communication.

Use `GET /api/v1/commands` for the current full definitions. [commands.json](commands.json) is an exported snapshot with all input/output schemas, TLV tags, exact types, enums, flags, preconditions, notes, and original Russian descriptions. [API.md](API.md) explains request formats and execution semantics.

For a configured `ffd1.2` device, use `GET /api/v1/commands?deviceId=...` or the [commands-ffd12.json](commands-ffd12.json) export. Its 93 entries contain 77 implemented public wire definitions (76 distinct command bytes), 10 explicitly unavailable entries, and six application commands, including FFD-only `registerKkt`, `createReceipt`, and `createCorrectionReceipt`. Registration bytes E1/E2/E3 are internal steps of `registerKkt`, preserving checksum validation before any registration mutation. The two report finalizers share byte `0x28` and have separate context-selected decoders. [FFD12.md](FFD12.md) contains the supplement audit matrix, FN closure, host-setting and diagnostic additions, receipt/report definitions, inherited commands, and remaining scope limits. `applyActivationCode` (`0xDD`) submits the vendor-supplied, device-specific 16-byte code as exactly 32 hexadecimal characters in `activationCodeHex`; it is a standalone journaled write requiring a request ID. Do not use the base table as a substitute for that device's actual schemas.

| Code | Canonical command | Category | Policy | Invocation | Protocol 01.05 page |
| --- | --- | --- | --- | --- | --- |
| `0x01` | `getStatus` | diagnostics | Read-only | Direct | 10 |
| `0x02` | `getSerialNumber` | identification | Read-only | Direct | 10 |
| `0x03` | `getFirmwareVersion` | identification | Read-only | Direct | 10 |
| `0x04` | `getModel` | identification | Read-only | Direct | 10 |
| `0x05` | `getFiscalStorageSerialNumber` | identification | Read-only | Direct | 10 |
| `0x06` | `getFiscalStorageFirmwareVersion` | identification | Read-only | Direct | 10 |
| `0x07` | `getFiscalStorageValidity` | diagnostics | Read-only | Direct | 10 |
| `0x08` | `getFiscalStorageStatus` | diagnostics | Read-only | Direct | 11 |
| `0x09` | `getFiscalStorageErrors` | diagnostics | Read-only | Direct | 12 |
| `0x0A` | `getRegistrationParameters` | diagnostics | Read-only | Direct | 11 |
| `0x0B` | `getConfigurationVersion` | identification | Read-only | Direct | 11 |
| `0x0E` | `getNetworkParameters` | diagnostics | Read-only | Direct | 12 |
| `0x10` | `cancelDocument` | documents | State-changing | Direct | 15 |
| `0x12` | `startRegistration` | registration | State-changing | Operation | 13 |
| `0x13` | `completeRegistration` | registration | State-changing | Operation | 13 |
| `0x14` | `beginFiscalStorageClosure` | unresolved | Unavailable | — | 7 |
| `0x15` | `finalizeFiscalStorageClosure` | unresolved | Unavailable | — | 7 |
| `0x16` | `sendRegistrationData` | registration | State-changing | Operation | 13 |
| `0x17` | `sendFiscalStorageClosureData` | unresolved | Unavailable | — | 7 |
| `0x19` | `addCashlessPaymentDetails` | receipts | State-changing | Operation | 19 |
| `0x1F` | `setReceiptDeviceDetails` | receipts | State-changing | Operation | 19 |
| `0x20` | `getShiftParameters` | shift | Read-only | Direct | 14 |
| `0x21` | `startOpenShift` | shift | State-changing | Operation | 14 |
| `0x22` | `completeOpenShift` | shift | State-changing | Operation | 15 |
| `0x23` | `beginReceipt` | receipts | State-changing | Operation | 16 |
| `0x24` | `finalizeReceipt` | receipts | State-changing | Operation | 19 |
| `0x25` | `beginCorrectionReceipt` | correction | State-changing | Operation | 20 |
| `0x26` | `finalizeCorrectionReceipt` | correction | State-changing | Operation | 21 |
| `0x27` | `beginSettlementReport` | reports | State-changing | Operation | 22 |
| `0x28` | `finalizeSettlementReport` | reports | State-changing | Operation | 22 |
| `0x29` | `startCloseShift` | shift | State-changing | Operation | 15 |
| `0x2A` | `completeCloseShift` | shift | State-changing | Operation | 15 |
| `0x2B` | `addReceiptItem` | receipts | State-changing | Operation | 16 |
| `0x2C` | `setReceiptAgentData` | receipts | State-changing | Operation | 18 |
| `0x2D` | `setReceiptPayment` | receipts | State-changing | Operation | 18 |
| `0x2E` | `setCorrectionData` | correction | State-changing | Operation | 20 |
| `0x2F` | `sendCashierData` | shift | State-changing | Operation | 14 |
| `0x30` | `findFiscalDocument` | archive | Read-only | Direct | 23 |
| `0x31` | `printOfdAcknowledgement` | archive | State-changing | Direct | 23 |
| `0x32` | `getUnconfirmedDocumentCount` | archive | Read-only | Direct | 23 |
| `0x33` | `getActivationTotals` | archive | Read-only | Direct | 24 |
| `0x34` | `getActivationParameter` | unresolved | Unavailable | — | 8 |
| `0x35` | `selectFiscalDocumentTLV` | archive | State-changing | Operation | 23 |
| `0x36` | `readFiscalDocumentTLV` | archive | State-changing | Operation | 23 |
| `0x39` | `printFiscalDocument` | archive | State-changing | Direct | 23 |
| `0x3A` | `readFiscalDocumentTags` | archive | Read-only | Direct | 14 |
| `0x3B` | `readRegistrationReportTags` | archive | Read-only | Direct | 14 |
| `0x3F` | `setCorrectionDeviceDetails` | correction | State-changing | Operation | 21 |
| `0x40` | `resetTestFiscalStorage` | debug | State-changing | Direct | 28 |
| `0x50` | `getOFDExchangeStatus` | diagnostics | Read-only | Direct | 12 |
| `0x61` | `printText` | printer | State-changing | Direct | 24 |
| `0x62` | `cutPaper` | printer | State-changing | Direct | 24 |
| `0x63` | `printQRCode` | printer | State-changing | Direct | 25 |
| `0x64` | `bufferFormattedText` | printer | State-changing | Operation | 26 |
| `0x65` | `printTextBuffer` | printer | State-changing | Operation | 26 |
| `0x69` | `printBarcode` | printer | State-changing | Direct | 25 |
| `0x72` | `setDateTime` | settings | State-changing | Direct | 29 |
| `0x73` | `getDateTime` | settings | Read-only | Direct | 29 |
| `0x74` | `setNetworkSettings` | settings | State-changing | Direct | 29 |
| `0x75` | `getNetworkSettings` | settings | Read-only | Direct | 29 |
| `0x76` | `setOfdSettings` | settings | State-changing | Direct | 30 |
| `0x77` | `getOfdSettings` | settings | Read-only | Direct | 29 |
| `0x78` | `setPrinterSettings` | settings | State-changing | Direct | 30 |
| `0x79` | `getPrinterSettings` | settings | Read-only | Direct | 30 |
| `0x80` | `setControlSettings` | settings | State-changing | Direct | 31 |
| `0x81` | `getControlSettings` | settings | Read-only | Direct | 31 |
| `0xB0` | `getCashCounters` | cashCounters | Read-only | Direct | 27 |
| `0xB1` | `resetCashCounters` | cashCounters | State-changing | Direct | 27 |
| `0xB2` | `printCashCountersReport` | cashCounters | State-changing | Direct | 27 |
| `0xB3` | `depositCash` | cashCounters | State-changing | Direct | 27 |
| `0xB4` | `withdrawCash` | cashCounters | State-changing | Direct | 27 |
| `0xBB` | `getPrintLineWidth` | diagnostics | Read-only | Direct | 12 |

## FFD 1.2 FN closure

Version 0.7.4 implements the following FFD-only operation. All steps require one complete `executeOperation` and one request ID. Direct calls return `400 operation_required`.

| Code | Canonical command | Policy | Invocation | Supplement page |
| --- | --- | --- | --- | --- |
| `0x14` | `beginFiscalStorageClosure` | State-changing | Operation | 13 |
| `0x17` | `sendFiscalStorageClosureData` | State-changing | Operation | 13 |
| `0x28` | `finalizeFiscalStorageClosure` | State-changing | Operation | 13 |

The minimal sequence is `0x14`/`0x28`; optional `0x17` supplies address/place and paired report attribute/data. Within the same operation, numeric `0x28` selects FN closure after `0x14`, or the existing settlement finalizer after `0x27`. Explicit named finalizers must match their start. Closure and settlement use strict eight-byte and 15-byte results respectively, chosen before communication. The named closure definition has no separate numeric alias; dispatch of `0x28` is contextual. FFD-only `finalizeFiscalStorageClosureLegacy` at `0x15` remains unavailable.

The entire operation is prevalidated and holds one device gate. A failure stops later steps without rolling back earlier effects or automatically canceling the report. A later failure after accepted state-changing work remains unknown and cannot be repeated automatically. [API.md](API.md#ffd-12-fn-closure-and-report-finalization) describes the full contract.

## FFD 1.2 host settings and diagnostics

These version 0.7.3 additions are available only under `ffd1.2`, independently of serial or TCP transport. Every getter takes empty parameters and performs a fresh read without journal records. Both setters require every named field and a request ID; neither requires an `executeOperation` wrapper. [API.md](API.md#ffd-12-host-settings-and-diagnostics) describes the returned fields and optional values.

| Code | Canonical command | Policy | Invocation | Supplement page |
| --- | --- | --- | --- | --- |
| `0x0F` | `getFiscalStorageFormat` | Read-only | Direct | 5 |
| `0x82` | `setOismSettings` | State-changing | Direct | 4 |
| `0x83` | `getOismSettings` | Read-only | Direct | 4 |
| `0x84` | `setMarkingKeyUpdateSettings` | State-changing | Direct | 4 |
| `0x85` | `getMarkingKeyUpdateSettings` | Read-only | Direct | 4 |
| `0x9D` | `getFiscalStorageMemory` | Read-only | Direct | 6 |
| `0x9E` | `getOismNotificationStatus` | Read-only | Direct | 5 |
| `0xA2` | `getServiceParameters` | Read-only | Direct | 28 |

OISM setters require `host`, `tcpPort`, and `timeoutSeconds`; key-update setters require `host` and `tcpPort`. Hosts are limited to 128 CP866 bytes; ports and the timeout are UINT16 LE. The recommended OISM timeout of 30 seconds is an explicit caller value. FN memory preserves omission of `notificationStorageUsedPercent`, and service parameters remain an opaque string.

## Application commands

| Command | Parameters | Behavior |
| --- | --- | --- |
| `executeOperation` | Ordered `commands` array | Executes a complete validated sequence under one device gate and one durable request ID; returns ordered results |
| `syncDateTime` | Empty object `{}` | Samples the Go server's local clock after queue waiting and any configured identity verification, drops seconds, and writes `0x72`/TLV `30000`; returns the sent `dateTime` |
| `closeShiftIfOpen` | Required `cashierName`, optional `cashierInn` and numeric `suppressPrint` | Checks `0x20` and closes an open shift with `0x29` → `0x2F` → `0x2A`. Both branches return `alreadyClosed` and `shiftNumber`. [API contract](API.md#close-the-shift-if-it-is-open) |
| `registerKkt` (FFD only) | Flat registration fields, report type, user/OFD INN, registration number, taxation mask, and conditional reason mask | Validates the request and actual KKT serial, then executes internal `0xE1`/`0xE2`/`0xE3` steps under one request ID; returns `documentNumber` and `fiscalSign` |
| `createReceipt` (FFD only) | Cashier, items, optional payment/additional data, calculation sign, and total | Checks shift state, conditionally opens a shift, creates the receipt, and reports any confirmed-document cleanup on failure |
| `createCorrectionReceipt` (FFD only) | Same receipt fields, with required `additionalData.correctedCalculationDate` | Checks/opens the shift, creates a correction using `0x25`/`0x96`/`0x98`/optional `0x2D`/`0x26`, and uses confirmed-document cleanup |

All application commands require a request ID and have no command byte of their own. Submit `closeShiftIfOpen` directly on either profile. The FFD-specific `registerKkt`, `createReceipt`, and `createCorrectionReceipt` also run as standalone requests. Both receipt workflows require a cashier name and 1–200 unmarked items, leave an opened shift open, and infer no payment data. Their recovery can cancel only a document confirmed opened by that request; a lost `0x24`/`0x26` result stays unknown even if cancellation succeeds. [API.md](API.md#create-a-correction-receipt-with-conditional-shift-opening) documents the correction workflow added in version 0.7.5. A batch containing only reads still has operation-level persistence. Repeating a completed `syncDateTime` request ID replays its saved timestamp rather than synchronizing again; use a new ID for each intended synchronization. Its timezone is the Go server process's local timezone, which can be set with `TZ`; no automatic schedule is created.

Every protocol command accepts its registered numeric alias, for example `0x03`, and lettered hexadecimal aliases such as `0x2B`/`0x2b`. `getSerialNumber` additionally accepts `ID`.

## FFD registration and re-registration

Version 0.7.6 adds the single external `registerKkt` application command. It validates every field before communication, reads the actual KKT factory serial (`0x02`) for the registration-number CRC, then executes `0xE1` → `0xE2` → `0xE3` with no interleaving. The three bytes are internal implementation steps and are not exposed through command aliases or `executeOperation`. Superseded base registration commands remain unavailable in the FFD profile.

`reportType` is `0` for initial registration, `1` for re-registration with FN replacement, or `2` for re-registration without replacement. `reregistrationReason` is omitted for initial registration and required as a four-byte UINT32 LE bitmask for re-registration. Bit 31 is `2147483648` (`0x80000000`); decimal `31` sets bits 0–4. Optional tags are `1203`, `1036`, `1274`/`1275` as a pair, `9998`, and `9999`. See [API.md](API.md#register-or-reregister-a-kkt) for all parameters, checksums, failure behavior, and wire padding.

## Unavailable definitions

| Code | Reason |
| --- | --- |
| `0x14` | Listed in the base protocol without a payload definition. The separate FFD 1.2 supplement is not silently substituted for the configured base protocol. |
| `0x15` | The base protocol provides no payload definition for `0x15`. The FFD 1.2 profile supports fiscal-storage closure with `0x14`, optional `0x17`, and context-dependent finalization `0x28`. |
| `0x17` | Listed without a payload definition in the base protocol. FFD 1.2-specific fields are not assumed to apply to this base protocol. |
| `0x34` | The command is listed, but its request and response layouts are not documented. |

These unavailable definitions describe the base profile, whose supplied protocol lacks sufficient request/response layouts. The separate FFD 1.2 profile implements `0x14` and `0x17` using supplement page 13 and resolves its shared `0x28` finalizer from the complete operation. Legacy `0x15` remains unavailable in both profiles.

## Representation and compatibility

- Quantities are exact decimal strings; kopeck fields use exact integer representations. Text is CP866 on the wire, with encoded-length checks.
- VAT rates `1`–`12` and correction tags `40007`–`40012` include the supplied protocol’s appendix-10 additions.
- `finalizeReceipt` decodes 10-, 15-, and 17-byte responses. `finalizeCorrectionReceipt` defaults to firmware/configuration `1.0.18+`; `legacyVatRate` explicitly opts into the older layout.
- `findFiscalDocument` preserves its type-specific document body as hexadecimal `documentData`. Archive TLV responses preserve ordered/repeated/unknown tags and exact `rawHex`.
- `selectFiscalDocumentTLV` and `readFiscalDocumentTLV` are state-changing because they select/advance shared cursor state; execute them together in one protected operation.
- `repeatUntilComplete: true` is available only on an operation’s `readFiscalDocumentTLV` step. It consumes the selected length, bounded to 2048 reads and 65535 bytes.
- Settings writes are complete forms, not patches: network settings require four fields; OFD settings allow only the fallback IP to be omitted; printer settings allow only the model to be omitted; control settings require interface and port.
- Printer models `0`–`10` follow the explicit Appendix 4 dictionary on page 36. Cash counter/deposit/withdrawal amounts remain exact integer kopecks.
- Switching `controlInterface` to Ethernet changes the KKT, not the driver's per-device configuration. It must be the final operation step. On TCP-configured devices, any `setNetworkSettings` or `setControlSettings` step must be last. Update the server's connection configuration separately when migrating.
- Current fields describe the supplied protocol, including historic VAT wording where present; the driver does not reinterpret that wording as current fiscal legislation.
