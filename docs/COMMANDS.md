# Command index

The default **base** catalogue exposes **74 entries**: **68 implemented base-protocol commands**, **four unavailable base-protocol entries**, and the two application commands **`executeOperation`** and **`syncDateTime`**. All **72 command entries listed in the supplied base protocol** are represented. The table below describes that default profile.

Read-only commands perform fresh device queries and are not journaled. State-changing commands require a `requestId`. **Operation** means the command must appear inside a complete `executeOperation`; **Direct** allows a standalone request. Unavailable entries return `501 command_unavailable` without device communication.

Use `GET /api/v1/commands` for the current full definitions. [commands.json](commands.json) is an exported snapshot with all input/output schemas, TLV tags, exact types, enums, flags, preconditions, notes, and original Russian descriptions. [API.md](API.md) explains request formats and execution semantics.

For a configured `ffd1.2` device, use `GET /api/v1/commands?deviceId=...` or the [commands-ffd12.json](commands-ffd12.json) export. Its 79 entries contain 65 implemented wire commands, 12 explicitly unavailable entries, and the two application commands. [FFD12.md](FFD12.md) contains the full supplement audit matrix, new `0x96`/`0x98`/`0xE4`/`0xE5`/`0xE6` definitions, inherited commands, and remaining scope limits. Do not use the base table as a substitute for that device's actual schemas.

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

## Application commands

| Command | Parameters | Behavior |
| --- | --- | --- |
| `executeOperation` | Ordered `commands` array | Executes a complete validated sequence under one device gate and one durable request ID; returns ordered results |
| `syncDateTime` | Empty object `{}` | Samples the Go server's local clock after queue waiting and any configured identity verification, drops seconds, and writes `0x72`/TLV `30000`; returns the sent `dateTime` |

Both application commands require a request ID and have no command byte of their own. A batch containing only reads still has operation-level persistence. Repeating a completed `syncDateTime` request ID replays its saved timestamp rather than synchronizing again; use a new ID for each intended synchronization. Its timezone is the Go server process's local timezone, which can be set with `TZ`; no automatic schedule is created.

Every protocol command accepts its registered numeric alias, for example `0x03`, and lettered hexadecimal aliases such as `0x2B`/`0x2b`. `getSerialNumber` additionally accepts `ID`.

## Unavailable definitions

| Code | Reason |
| --- | --- |
| `0x14` | Listed in the base protocol without a payload definition. The separate FFD 1.2 supplement is not silently substituted for the configured base protocol. |
| `0x15` | The base protocol provides no payload definition; the FFD 1.2 supplement uses a conflicting command code. Manufacturer clarification is required. |
| `0x17` | Listed without a payload definition in the base protocol. FFD 1.2-specific fields are not assumed to apply to this base protocol. |
| `0x34` | The command is listed, but its request and response layouts are not documented. |

The supplied base protocol lists these commands without enough request/response information to implement them. The separate FFD 1.2 supplement contains conflicting closure codes; the driver requires clarification instead of silently combining incompatible definitions.

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
