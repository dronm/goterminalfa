# Architecture

`goterminalfa` runs as one Linux service. Its HTTP API, command catalogue, and protocol logic are separate from the physical transport.

| Component | Responsibility |
| --- | --- |
| Configuration | Validate HTTP, logging, database, and per-device settings |
| HTTP API | Decode JSON, apply authentication and limits, map results to responses |
| Command catalogue | Define names, aliases, input/output schemas, documentation, and policy |
| Device execution | Bound waiting requests and serialize complete operations per physical KKT |
| Operation executor | Validate full workflows and execute ordered commands under one device gate |
| Protocol/codec | Encode CP866, integers, exact decimals, TLV/STLV, and CRC-framed packets |
| Serial transport | Open a session, exchange commands with deadlines, and close the port |
| TCP transport | Connect, write a frame, read/validate its response, and close for each command |
| SQLite journal | Claim state-changing operations and retain canonical inputs and results |

## Device ownership

Each configured device has its own logical ID, connection settings, execution gate, and bounded waiting capacity. Independent devices can execute concurrently. Waiting consumes the HTTP request deadline; strict arrival order is not guaranteed.

Only one active controller should operate a physical KKT, even when separate processes use different database files or address it over different transports. The journal process lock does not provide a cross-database hardware lock. Configuration rejects duplicate IDs, expected serials, serial paths, and TCP endpoints. Existing serial symlinks are resolved where possible; absent paths, DNS aliases, and mixed transports cannot always be compared by eventual physical identity.

Keep logical IDs assigned to the same physical KKT across restarts. Remapping an ID remaps its journal scope; renaming an ID creates a different scope.

When an expected factory serial is configured, state-changing execution verifies identity before invoking its mutating handler. Serial verification and mutation use the same physical session. For TCP, verification is a separate command connection to the configured endpoint; it is not proof that every later socket reaches the same device if that endpoint changes. Use a static IP or a DHCP reservation, and keep the endpoint assigned to one physical KKT. The serial-number query itself also rejects an unexpected result. Identity checks distinguish the KKT factory serial from its USB descriptor serial.

## Connection and operation lifetime

A standalone serial command opens one connection and closes it after the exchange. A serial `executeOperation` retains that connection and one device gate for its complete sequence, including identity verification when configured.

TCP opens a new connection for every protocol command, including commands inside `executeOperation`. It dials, writes one binary frame, reads and validates one complete response, and closes. The operation retains its device gate across all of these connections. Connection lifecycle therefore differs by transport while scheduling and idempotency semantics remain the same.

The operation catalogue validates every step before communication and normalizes aliases before idempotency fingerprinting. It requires matching start/data/completion sequences for registration, shifts, receipts, correction, reports, and archive cursors. Formatted text must be consumed by `printTextBuffer` or `finalizeReceipt` within the same operation. Those stateful subcommands cannot be submitted as independent HTTP requests. One operation's commands cannot interleave with another request to the same KKT.

A completed operation is not an atomic device transaction. Finalizing a document or printing cannot be undone by closing the connection. If a later step fails after state-changing work succeeded, the overall outcome is unknown and its request ID is not executed again.

Operation sessions use the callback-scoped `Client.WithSession` API. The scoped client must not outlive its callback; use it, rather than the parent client, for calls inside that session. A serial session holds a physical port; a TCP session preserves logical operation ownership while each exchange uses its own connection.

## Per-device transports

Transport is selected per device as `serial` or `tcp`; both can coexist in one service. The same command catalogue and HTTP/JSON API apply. The KKT Ethernet protocol is raw binary TCP, not HTTP; the host opens a fresh TCP connection for each command as specified on protocol page 6.

Automatic command retries and transport failover are absent. A timeout does not establish whether the device executed a command, so sending it again through either transport would require reconciliation.

## Protocol and parameter validation

Frames start with `B6 29`. The two-byte length is big-endian and includes the command/result byte. CRC16-CCITT uses polynomial `0x1021`, initial `0xFFFF`, and covers length plus command/result plus data. The CRC is little-endian.

The decoder checks result codes, lengths, CRC, and expected field structure; it handles fragmented reads. Codecs preserve exact integer/decimal quantities, distinguish absent fields from zero, validate CP866 lengths, and support repeated/nested TLV data. Archive TLVs preserve unknown tags. Command `0x30` keeps its document-type-dependent body opaque rather than guessing a layout.

Each exchange has a response deadline within the complete request deadline, so partial response traffic cannot extend waiting indefinitely. Cancellation interrupts response reads by closing the port. Serial opening, configuration, and writes are synchronous OS/library calls that cannot be forcibly interrupted by the context deadline; the code checks cancellation before writes and avoids concurrent raw-descriptor closing during them.

TCP adds a dial timeout and a whole-exchange deadline covering connect, write, and read. The HTTP request deadline includes queue waiting and all command exchanges in the operation. A shorter HTTP deadline takes precedence over a longer transport deadline.

## Durable idempotency

Read-only commands bypass the journal and query the device afresh. Every implemented state-changing command, including printing and archive cursor advancement, requires a request ID. `executeOperation` always uses one durable claim for its whole batch.

SQLite atomically claims `(deviceId, requestId)`, storing canonical command/parameters and their fingerprint. Concurrent duplicate claims cannot both acquire execution rights. Successful and definite failed results are retained. Known failures before a mutating handler starts, plus TCP failures proven to occur before any command bytes were written, remain definite failures. Partial TCP delivery or a lost response is not proof that the device rejected the operation.

| Stored state | Meaning | Matching duplicate |
| --- | --- | --- |
| `in_progress` | Claimed operation is being handled | Report in progress |
| `completed` | Original HTTP result is recorded | Replay original status and body |
| `unknown` | Execution outcome is unresolved | Report unknown; do not execute again |

A changed payload under an existing identity is a conflict. A matching completed result is replayed even if an alias was used in the original request. A batch failure after successful state-changing work remains unknown even when the later step returned a definite rejection.

A process lock beside the database protects recovery from another service instance using that journal. Startup marks interrupted `in_progress` records unknown rather than assuming failure. A disconnected caller does not cancel completed effects; result persistence has its own bounded context after execution.

The journal is not a durable asynchronous work queue. Requests execute synchronously with bounded in-process waiting. Unknown fiscal outcomes require reconciliation against the KKT state and document archive before further action.

## Catalogue scope and compatibility

The catalogue lists all 72 commands in the supplied base protocol: 68 implemented and four unavailable. The application commands `executeOperation` and `syncDateTime` bring discovery to 74 entries. Unavailable definitions state why they cannot be encoded safely; the separate FFD 1.2 supplement is not mixed into conflicting base-protocol command numbers.

`syncDateTime` validates only an empty parameter object before queueing. It samples the Go process's local wall clock at runtime inside the device gate, after any configured identity check, rather than during request prevalidation. The sampled minute-precision value is encoded through the standard `setDateTime` (`0x72`, TLV `30000`) path and written once. Its request ID and result use the existing mutation journal; replay returns the previous timestamp without resampling or another device write. The process clock and local timezone determine the value, with no browser/caller timezone conversion or scheduled synchronization.

Descriptions preserve Russian protocol wording and include source pages, schemas, enums, bitmasks, notes, and prerequisites. Appendix-10 VAT and correction additions are included. Firmware-specific receipt layouts are handled explicitly; the optional legacy correction-VAT byte is opt-in.

Device settings use named TLV schemas with explicit presence rules rather than partial-update defaults. The printer model dictionary uses the documented values 0–10 from Appendix 4. Reading settings and counters is read-only; writing settings, resetting counters, printing reports, and cash deposits/withdrawals are journaled. A control-interface change to Ethernet must be the final operation step, because subsequent serial exchanges may no longer be possible. On a TCP-configured device, any network-settings or control-settings write must be last to avoid continuing through a possibly changed endpoint. These writes do not edit the Go service's transport configuration.

Tests use simulated transports and temporary databases. They exercise codecs, documented frame vectors, session ownership, workflow validation, concurrency, and journal behavior without sending commands to physical hardware. Physical-device acceptance should begin with read-only identification and diagnostic commands.
