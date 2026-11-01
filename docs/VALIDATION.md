# Validation

Version 0.5.0 was validated on Linux amd64 with Go 1.25.3. The module requires Go 1.23 or newer. These checks passed:

```bash
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build ./cmd/goterminalfa
```

Normal builds use the pure-Go SQLite driver. Race detection has the Go toolchain's separate C compiler requirement.

- The catalogue covers all 72 command codes listed in the supplied base protocol: 68 implemented and four explicitly unavailable because the base protocol omits their payload definitions. The application commands `executeOperation` and `syncDateTime` bring discovery to 74 entries.
- Request/response tests cover each command group, positional and TLV/STLV data, CP866 byte lengths, exact integer and decimal values, calendar dates, conditional fields, repeated tags, historical response lengths, and the amendments in Appendix 10.
- Workflow tests cover complete fiscal sequences, serial session ownership, per-device exclusivity, print-buffer consumption by receipt finalization, registration reason consistency, and bounded archive draining.
- SQLite tests cover canonical request persistence, exact response replay, conflicts, concurrent duplicates, startup recovery, definite pre-execution failures, and unknown outcomes that cannot be automatically repeated.
- Status and cash-counter tests cover the 21-byte KKT status, printed-line width, exact five-byte counter amounts, resets/reports, and variable-length deposits/withdrawals.
- Settings tests cover TLV getters/setters, required complete forms, optional OFD fallback IP and printer model, IPv4/mask validation, CP866 text limits, unknown response tags, explicit zeros, and the printer dictionary's values 0–10.
- Operation validation restricts a switch to Ethernet control to the final batch step. For TCP-configured devices, any network-settings or control-settings write must be last, preventing later steps from assuming the previous endpoint remains available.
- Deterministic clock-sync tests cover runtime sampling after queue waiting and identity verification, a non-UTC timezone near midnight, dropped seconds, exact `0x72`/TLV `30000` encoding, and supported year boundaries 2000/2255. Unsupported years 1999/2256 produce a definite failure without a clock write.
- Clock-sync replay tests verify no resampling or device call for a repeated request ID, durable replay through a new service/catalogue, and a fresh sample for a new intended request ID. Device rejection, definite unsent failure, lost replies, and timeouts retain the expected journal states.
- Clock-sync operation tests cover mutation tracking and rejection of an invalid later step before clock sampling or device traffic. HTTP tests cover public validation, command discovery, and replay.

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

All 18 supplied JSON command examples passed catalogue/workflow validation using a guaranteed nonexistent serial path. The serial, TCP, and mixed configuration examples also passed actual server startup checks without opening a device connection. These checks do not transmit physical commands. State-changing templates have not been executed against a registered KKT.

## Mixed serial/TCP end-to-end simulation

A compiled version 0.5.0 server passed a mixed-device simulation using the real serial library against a Linux pseudo-terminal and raw TCP against a loopback fake KKT. It verified:

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

TCP unit tests additionally cover short writes, zero-byte unsent failures, dial and whole-exchange deadlines, cancellation, connection cleanup, protocol rejection, and invalid configuration. The final command catalogue was exported through the compiled server's HTTP API and contains 74 entries: 68 implemented wire commands, four explicitly unavailable definitions, and two application commands.

The TCP command stream is raw KKT protocol data, not an HTTP exchange with the device. Loopback TCP simulations and serial pseudo-terminals do not establish physical LAN compatibility; follow [LAN.md](LAN.md) for a read-only hardware acceptance check.

## Hardware boundary

No physical KKT was accessible during implementation. The earlier serial-number response capture is retained as a protocol vector. Fiscal operations and firmware-specific behavior still require hardware acceptance using the appropriate registered device or test fiscal storage. Validation does not substitute for device checks of registered tax regimes, fiscal-storage state, or other runtime prerequisites documented in the manual.
