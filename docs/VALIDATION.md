# Validation

## Context-selected FN closure (version 0.7.4)

Version 0.7.4 adds FFD FN closure through `0x14`, optional `0x17`, and context-selected `0x28`. The catalogue contains 90 entries: 77 implemented wire definitions covering 76 distinct command bytes, 10 unavailable entries, and three application commands. Closure and settlement have separate definitions for the shared finalizer byte. Base execution behavior remains unchanged; the legacy `0x15` unavailability description is corrected.

Regression tests cover minimal and data-bearing closure, numeric/named canonicalization and durable replay across service restarts, one serial session for the complete operation, matching report contexts, strict eight-byte closure and 15-byte settlement responses, cancellation/reset boundaries, mixed-workflow rejection before traffic, and failure stopping later steps without rollback. A later error after accepted state-changing work remains unknown and does not trigger another execution. [The closure example](../examples/ffd12-close-fiscal-storage.json) is a deliberate mutation template, not a physical-device test.

Validated on Linux amd64 with Go 1.25.0: `go test -mod=readonly ./...`, `go vet -mod=readonly ./...`, and a `CGO_ENABLED=0` production build all passed. The source-archive build used `-buildvcs=false`; the executable reported `goterminalfa 0.7.4`.

A compiled-server simulation over fragmented, CRC-framed loopback TCP verified closure and settlement sequences, exact closure TLVs, their separate eight-byte and 15-byte finalization results, named replay, multiple report types in one operation, and context reset after explicit cancellation. Rejected closure/settlement data and a lost closure-finalization response stopped all subsequent commands, retained unknown outcomes, and caused no retry or automatic cancellation. Both runtime catalogues were exported: 85 unrelated FFD definitions remained unchanged, and the base catalogue changed only the legacy `0x15` explanation. The simulation used temporary configuration and journal files and no physical KKT; the server exited cleanly.

## FFD host settings and diagnostics (version 0.7.3)

Version 0.7.3 added the FFD-only OISM and marking-key-update settings pairs (`0x82`–`0x85`), FN-format (`0x0F`), OISM-notification (`0x9E`), FN-memory (`0x9D`), and opaque service-parameter (`0xA2`) reads. At that version the catalogue contained 89 entries: 74 implemented wire commands, 12 unavailable entries, and three application commands. The default base catalogue was unchanged.

Regression tests cover exact TLV tags and encoded bounds, complete setter forms, the explicit OISM timeout, notification zero dates, optional memory-byte omission, CP866 service text, FFD-only discovery, fresh unjournaled getters, and journaled setter replay. Eight individual JSON examples distinguish six reads from two deliberate settings writes. Example `.invalid` hosts and port `443` are placeholders; they are not production endpoints or device defaults.

Validated on Linux amd64 with Go 1.25.0: `go test -mod=readonly ./...`, `go vet -mod=readonly ./...`, and a `CGO_ENABLED=0` production build all passed. The source-archive build used `-buildvcs=false`; the executable reported `goterminalfa 0.7.3`. All documented JSON examples also passed catalogue validation after the eight new examples were added.

A compiled-server simulation over loopback TCP verified all eight command bytes and their CRC-framed exchanges, exact setter TLVs after factory-serial verification, setter replay without another write, and fresh reads for all six getters with repeated request IDs. It checked both notification-date forms, both FN-memory response lengths, and CP866 service text including whitespace. The test used a temporary configuration and journal, with no physical KKT connected. Runtime discovery regenerated `commands-ffd12.json` with 89 entries; all previous 81 FFD definitions and the base catalogue remained unchanged.

## Activation-code submission (version 0.7.2)

At version 0.7.2, the `ffd1.2` catalogue contained 81 entries: 66 implemented wire commands, 12 explicitly unavailable entries, and three application commands. The added `applyActivationCode` (`0xDD`) is a standalone journaled write; the base catalogue was unchanged.

Activation regression tests cover exact 16-byte wire encoding, rejection of invalid codes and missing request IDs before device communication, hexadecimal aliases, FFD-only discovery, and case-normalized idempotency replay. Error cases cover device rejection and uncertain outcomes after lost responses. These checks use simulated devices and temporary journals; they do not establish acceptance of a vendor code by a physical KKT.

Validated on Linux amd64 with Go 1.25.0: `go test -mod=readonly ./...`, `go vet -mod=readonly ./...`, and a `CGO_ENABLED=0` production build all passed. The source-archive build used `-buildvcs=false`; the executable reported `goterminalfa 0.7.2`.

A compiled-server simulation over loopback TCP verified the actual protocol framing, factory-serial verification, one exact 16-byte `0xDD` write, alias/case replay without another write, and rejection of malformed, missing-ID, base-profile, and wrong-serial requests before activation. Runtime discovery regenerated `commands-ffd12.json`; all previous FFD definitions and the base catalogue remained unchanged. The simulation used a temporary configuration and journal, and no physical KKT was contacted.

## Firmware 204 status compatibility (version 0.7.1)

The status compatibility update was checked on Linux amd64 with Go 1.25.3:

```bash
go test ./...
go vet ./...
GOFLAGS=-buildvcs=false make prod
./goterminalfa -version
```

All checks passed, and the production executable reported `goterminalfa 0.7.1`. VCS stamping was disabled for the source-archive build check.

Regression tests replay all three CRC-framed status responses captured from firmware `204` on 2026-11-12 through the frame reader and command decoder, in both `base` and `ffd1.2` profiles. They verify the known field offsets and the optional opaque `statusExtensionHex` value. Coverage retains the unchanged legacy 21-byte JSON response, preserves additional byte values `00` and `ff`, rejects truncated/oversized status responses and invalid dates, and enforces the configured factory serial for both response lengths. Other commands retain their existing strict trailing-byte checks.

An additional compiled-server check used Linux pseudo-terminals and the real serial transport to replay the captured 10:51 response. Both profiles returned HTTP 200 with `status: "completed"`, `dateTime: "2026-11-12T10:51"`, and `statusExtensionHex: "00"`. Runtime command discovery matched the updated exported catalogue metadata. This was a simulation using captured bytes; the updated binary was not connected to the physical KKT during these checks.

The extra byte's meaning remains undocumented. Compatibility treats the observed response as the documented 21-byte prefix plus one opaque trailing byte; it does not assign that byte a status flag or date/time meaning.

## Version 0.7.0 baseline

Version 0.7.0 was validated on Linux amd64 with Go 1.25.3. The module requires Go 1.23 or newer. These checks passed:

```bash
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build ./cmd/goterminalfa
```

Normal builds use the pure-Go SQLite driver. Race detection has the Go toolchain's separate C compiler requirement.

- The unchanged default base catalogue covers all 72 command codes listed in the base protocol: 68 implemented and four explicitly unavailable. The two application commands bring base discovery to 74 entries. The explicit FFD 1.2 catalogue contains 80 entries: 65 implemented wire commands, 12 explicitly unavailable entries, and three application commands, including FFD-only `createReceipt`.
- Request/response tests cover each command group, positional and TLV/STLV data, CP866 byte lengths, exact integer and decimal values, calendar dates, conditional fields, repeated tags, historical response lengths, and the amendments in Appendix 10.
- Workflow tests cover complete fiscal sequences, serial session ownership, per-device exclusivity, print-buffer consumption by receipt finalization, registration reason consistency, and bounded archive draining.
- SQLite tests cover canonical request persistence, exact response replay, conflicts, concurrent duplicates, startup recovery, definite pre-execution failures, and unknown outcomes that cannot be automatically repeated.
- Status and cash-counter tests cover the 21-byte KKT status, printed-line width, exact five-byte counter amounts, resets/reports, and variable-length deposits/withdrawals.
- Settings tests cover TLV getters/setters, required complete forms, optional OFD fallback IP and printer model, IPv4/mask validation, CP866 text limits, unknown response tags, explicit zeros, and the printer dictionary's values 0–10.
- Operation validation restricts a switch to Ethernet control to the final batch step. For TCP-configured devices, any network-settings or control-settings write must be last, preventing later steps from assuming the previous endpoint remains available.
- Deterministic clock-sync tests cover runtime sampling after queue waiting and identity verification, a non-UTC timezone near midnight, dropped seconds, exact `0x72`/TLV `30000` encoding, and supported year boundaries 2000/2255. Unsupported years 1999/2256 produce a definite failure without a clock write.
- Clock-sync replay tests verify no resampling or device call for a repeated request ID, durable replay through a new service/catalogue, and a fresh sample for a new intended request ID. Device rejection, definite unsent failure, lost replies, and timeouts retain the expected journal states.
- Clock-sync operation tests cover mutation tracking and rejection of an invalid later step before clock sampling or device traffic. HTTP tests cover public validation, command discovery, and replay.
- FFD profile tests cover default-base compatibility, per-device catalogue selection, profile-aware fingerprints, strict shift/receipt result sizes, flat unmarked item TLVs, additional receipt/correction data, optional shift/report data, and rejection of superseded legacy fiscal formats.
- The supplement's 13 overlapping command bytes and implemented additions were audited for version 0.7.0. Registration, marking, and then-unimplemented closure workflows were outside that version's subset; these baseline checks did not establish full supplement coverage. Current scope is recorded in [FFD12.md](FFD12.md).
- Receipt-workflow tests cover conditional shift opening, preserved item order, optional data omission, required cashier validation even on the open-shift branch, and full preflight rejection of invalid or over-200-item requests. Unknown shift states start no mutation, and `createReceipt` is rejected outside FFD or when nested in a static operation.
- Cleanup tests cover definite versus ambiguous failures, acknowledgment-based ownership, fresh serial sessions, detached contexts after caller cancellation, a device gate held through cleanup, and refusal to cancel a different physical factory identity. Original failure details remain separate from cleanup errors.

## Command and serial coverage

The automated suite and serial simulations use temporary databases, Linux pseudo-terminals, the real serial library, and binary CRC-framed responses. Coverage includes:

- Existing `ID` behavior and hexadecimal command aliases.
- Independent device serials, firmware and CP866 model text.
- Clock TLV reads and writes.
- Durable mutation replay and parameter conflicts with no extra wire commands.
- A complete shift operation, with identity verification on its session.
- A failure after an earlier state-changing command, stored as an unknown outcome.
- Archive selection and repeated TLV reads until the declared document length was consumed.
- Explicit printer-not-ready data in a successful protocol response.
- Rejection of unsupported or individually submitted workflow commands.
- All 15 status, print-width, cash-counter, and settings command additions, with stateful counter updates and settings round trips.
- CP866 settings text, exact cash amounts, mutation replay without extra wire commands, and conflicting request parameters.
- Restricting a switch to Ethernet control to the final batch step, plus a lost switch response recorded as unknown and blocked from repetition.

All 28 supplied JSON command examples passed catalogue/workflow validation: 18 base examples and ten FFD-specific examples. The serial, TCP, mixed, and FFD configuration examples also passed actual version 0.7.0 server startup checks without opening a device connection. These checks do not transmit physical commands. State-changing templates have not been executed against a registered KKT.

## Mixed serial/TCP transport coverage

Transport tests and compiled mixed-device simulations use the real serial library against Linux pseudo-terminals and raw TCP against loopback fake KKT devices. Coverage includes:

- No device connection during startup, correct transport types in device discovery, and unchanged `ID` requests over both transports.
- TCP identity verification on its own command connection before mutation, a fresh connection for every command, and fragmented response handling without waiting for the peer to close its socket.
- One device gate across a complete shift operation's three TCP connections: a queued TCP request could not interleave, while a serial request to a different device completed independently.
- Exact SQLite result replay and changed-parameter conflicts without extra device calls; read-only calls created no journal rows.
- Device-aware network/control settings guards rejected invalid batches before claims or wire traffic.
- Expected-serial mismatches were stored as definite pre-execution failures. Refused offline TCP connections were also definite failures, with no automatic retry.
- Lost mutation replies produced unknown outcomes and blocked repeated execution.
- Corrupt CRC responses produced `502`, response deadlines produced `504`, and a later fresh read recovered using a new connection without an automatic retry of the failed command.
- `syncDateTime` worked over both serial and TCP with the Go process running under `TZ=Asia/Yekaterinburg`: its minute-precision payload and returned timestamp matched the current server-local target, and replay caused no additional wire commands.
- Graceful shutdown on `SIGTERM`.

TCP unit tests additionally cover short writes, zero-byte unsent failures, dial and whole-exchange deadlines, cancellation, connection cleanup, protocol rejection, and invalid configuration. Both final catalogues were exported through the compiled server's HTTP API: [commands.json](commands.json) preserves the base view and [commands-ffd12.json](commands-ffd12.json) contains the selected FFD view.

The TCP command stream is raw KKT protocol data, not an HTTP exchange with the device. Loopback TCP simulations and serial pseudo-terminals do not establish physical LAN compatibility; follow [LAN.md](LAN.md) for a read-only hardware acceptance check.

## FFD 1.2 compiled-server simulation

A compiled version 0.7.0 server passed a simulation with an FFD serial device, an FFD TCP device, and a base TCP device configured together. The run repeated the existing profile/transport checks and verified:

- Requested opening/closing sequences with inherited `0x2F`, exact 11-byte FFD completions, preserved `warningFlags`, and fresh `0x20` reads.
- Optional receipt printing suppression as UINT16 `41029`, flat CP866 unmarked-item TLVs through `0x96`, FFD payment data, and strict 17-byte finalization results.
- Complete correction with `0x98` after payment, confirming that the driver imposes no unsupported payment/additional-data ordering constraint while still requiring the correction date before finalization.
- Optional E4/E5/E6 report details, inherited cancellation, and continued mutation policy for clock/network/OFD/control setters `0x72`/`0x74`/`0x76`/`0x80`.
- Pre-wire rejection of invalid legacy parameters, base-profile `0x96` returning unknown-command, and FFD-profile `0x2B` returning unavailable-command. Base shift completions retained their ten-byte format.
- Exact mutation replay without extra wire traffic, lost finalization replies becoming unknown and blocking repeats, journal state persistence, a fresh connection per TCP command, and graceful shutdown.

Focused HTTP tests also passed for device-specific catalogue/error behavior and malformed base-length replies on FFD operations retaining a durable unknown outcome.

## Conditional receipt creation and cleanup

The compiled mixed-device simulation additionally verified `createReceipt` over both serial and TCP:

- Closed shifts followed identity `0x02`, state `0x20`, conditional `0x21`/`0x2F`/`0x22`, and receipt `0x23`/`0x96`/`0x2D`/`0x24` order. Already open shifts skipped the shift-opening commands.
- Successful request replay produced no additional device commands or printed receipt.
- A definite item rejection closed the primary serial session, opened fresh cleanup, rechecked identity, and acknowledged `0x10`. The response was a definite failure with original `data.failure` details and successful cleanup diagnostics; replay was exact on both transports.
- A lost TCP finalization reply was followed by fresh identity/cancellation acknowledgments, yet the operation remained unknown. Failure/cleanup details persisted in SQLite and a repeated request did not execute again.
- Cleanup retained per-device exclusivity and did not use a canceled primary context. Original device rejection details were preserved independently of any cleanup error, and completed results remained distinct from unknown journal claims.

The new example preserves normal printing defaults. Client and graceful-shutdown budgets include primary execution, the detached 25-second cleanup budget, and result persistence; the FFD example uses a 60-second primary request timeout and a 95-second shutdown timeout. These tests establish the reported cleanup policy, not a rollback guarantee for a potentially finalized fiscal receipt.

## Hardware boundary

No physical KKT was accessible during implementation. The earlier serial-number response capture is retained as a protocol vector. Fiscal operations and firmware-specific behavior still require hardware acceptance using the appropriate registered device or test fiscal storage. Validation does not substitute for device checks of registered tax regimes, fiscal-storage state, or other runtime prerequisites documented in the manual.
