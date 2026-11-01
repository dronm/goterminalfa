# LAN setup and TCP transport

The KKT command interface over Ethernet uses **raw binary TCP, not HTTP**. Protocol version 01.05, pages 5–6, uses the same `B6 29` framing, payloads, result codes, and CRC as serial. For each protocol command the host opens a TCP connection, sends the frame, reads the response, and closes.

Your applications still call the driver's HTTP/JSON API. A device's configured transport determines how the driver reaches that KKT. Serial and TCP devices can coexist in the same server.

## Power and network

Connect the KKT's Ethernet port to a network reachable by the Go server. Keep the unit powered according to its specifications. The operating manual, page 4, describes 5 V power through mini-USB: when using LAN, USB can supply power without carrying command data to the server. Use a suitable device-rated power source. Ethernet alone is not documented as a power source.

Use a static IP or a DHCP reservation, as recommended on protocol page 6. The endpoint must continue to identify the same physical KKT across all command connections. One controller should own each physical device, including when USB and Ethernet are both connected.

Use the KKT TCP interface on a trusted LAN or through a secure tunnel. The documented device interface has no TLS layer; the Go server's HTTP bearer token protects its API, not the KKT's raw TCP port.

## 1. Read the existing settings through working USB

Keep the driver's current serial configuration while inspecting the KKT. These are read-only queries; add an HTTP bearer header when configured.

Read stored network settings and the DHCP flag (`0x75`):

```bash
curl --fail-with-body --silent --show-error \
	-H 'Content-Type: application/json' \
	--data '{"deviceId":"cashdesk-1","cmd":"getNetworkSettings","params":{}}' \
	http://127.0.0.1:8080/api/v1/commands
```

Read the currently active Ethernet address (`0x0E`), especially when DHCP is enabled:

```bash
curl --fail-with-body --silent --show-error \
	-H 'Content-Type: application/json' \
	--data '{"deviceId":"cashdesk-1","cmd":"getNetworkParameters","params":{}}' \
	http://127.0.0.1:8080/api/v1/commands
```

Read the control-interface selection and command port (`0x81`):

```bash
curl --fail-with-body --silent --show-error \
	-H 'Content-Type: application/json' \
	--data '{"deviceId":"cashdesk-1","cmd":"getControlSettings","params":{}}' \
	http://127.0.0.1:8080/api/v1/commands
```

`getControlSettings` returns `controlInterface` and `tcpPort`. The port corresponds to tag `30032`; it is the KKT command-server port, not an assumed HTTP port or necessarily the OFD port. **`7778` in the supplied configuration is only an example, not a manufacturer default.**

## 2. Configure networking if needed

`setNetworkSettings` (`0x74`) takes a request ID and all four fields: `dynamicAddress`, `ipAddress`, `subnetMask`, and `gateway`. IPv4 values are dotted-decimal strings. Fields remain required when `dynamicAddress` is `1`; the driver accepts `0.0.0.0` placeholders for that mode. See [../examples/set-network-settings.json](../examples/set-network-settings.json).

For a static address, use a real unused address, contiguous mask, and appropriate gateway for your network. For DHCP, reserve the assigned address in your network configuration. Do not treat the example address or placeholders as a deployed static configuration.

Read the settings/current address again and record the address that the server will use. These operations configure the KKT; they do not edit the Go server's JSON configuration.

## 3. Select Ethernet control

`setControlSettings` (`0x80`) selects `controlInterface: 1` for Ethernet and requires an explicit command `tcpPort`. Submit it as a deliberate standalone state-changing request, or as the final step of an `executeOperation`. An illustrative request is:

```json
{
	"deviceId": "cashdesk-1",
	"cmd": "setControlSettings",
	"requestId": "switch-control-to-lan-001",
	"params": {
		"controlInterface": 1,
		"tcpPort": 7778
	}
}
```

Replace the port with your chosen, recorded device setting. This request can move control away from the working USB path. If the reply is lost during the switch, the operation may be stored as `unknown`. Do not repeat the mutation blindly or change its request ID merely to force another execution. Verify the new endpoint with read-only queries and reconcile the device's state.

The driver does not automatically switch transport, rewrite its config, retry through another connection, or fail over to USB. Once a device is configured with TCP, any network-settings or control-settings write must be the final batch step, because it can change the address, port, or interface. Standalone setting writes remain allowed with request IDs.

## 4. Configure the server's TCP device

Start from [../config.tcp.example.json](../config.tcp.example.json) or replace only the existing device's connection object:

```json
{
	"type": "tcp",
	"address": "192.0.2.50:7778",
	"connectTimeout": "5s",
	"responseTimeout": "25s"
}
```

Use the actual KKT address and command port. `address` accepts an IP or hostname with a numeric port; bracket an IPv6 literal, for example `[2001:db8::50]:7778`. It is a TCP endpoint and must not start with `http://` or another URL scheme. The base protocol's device-network settings use IPv4; acceptance of an IPv6 endpoint by the host driver does not establish IPv6 support in the KKT itself.

Keep the logical `deviceId`, expected factory serial, and database assigned to the same physical unit when migrating transport. Restart the server with the edited config. Do not run a second controller against that KKT in parallel. Existing serial configurations and clients require no changes for devices staying on USB.

[../config.mixed.example.json](../config.mixed.example.json) illustrates one serial unit and a different TCP unit in one process. Its second expected serial is omitted deliberately; configure it once you know that unit's identity. Do not duplicate a physical unit as separate USB and TCP entries.

## 5. Test with the existing read-only request

After restart, use the same logical ID and HTTP API:

```bash
curl --fail-with-body --silent --show-error \
	-H 'Content-Type: application/json' \
	--data '{"deviceId":"cashdesk-1","cmd":"ID","params":{}}' \
	http://127.0.0.1:8080/api/v1/commands
```

Confirm the returned factory serial number. Then query `getControlSettings` or other read-only diagnostics through TCP. A successful read verifies the selected endpoint without repeating the switch mutation; it does not automatically resolve an existing unknown journal record.

## Timeouts and operation ownership

`connectTimeout` bounds dialing. `responseTimeout` bounds the complete TCP exchange from connect through write and full response read; network cancellation interrupts reads and writes. The HTTP request timeout includes queue waiting and every command in a batch; configure it and the client's timeout for the complete intended operation. Serial retains its OS/library limitation: context cancellation cannot forcibly interrupt synchronous opening, configuration, or writing.

TCP opens a fresh connection for every command, even inside `executeOperation`. One device gate and one idempotency claim still cover the entire operation, so requests from other apps cannot interleave between its commands. Serial retains one physical port for the complete operation.

When configured, TCP identity verification reads the expected factory serial at the endpoint before a mutating operation; subsequent protocol commands use separate sockets. A stable endpoint and single-controller ownership remain essential. A lost response has the same idempotency and unknown-outcome rules as serial; TCP adds no automatic retry or failover behavior.
