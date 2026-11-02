# Validation

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
- The supplement's 13 overlapping command bytes and implemented additions are audited in [FFD12.md](FFD12.md). Registration, marking, and ambiguous closure workflows remain outside the implemented subset; tests do not treat the profile as full supplement coverage.
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
