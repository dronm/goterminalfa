package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configFile(t *testing.T, modify func(map[string]any)) string {
	t.Helper()
	object := map[string]any{
		"devices": []any{map[string]any{
			"id": "kkt-1", "expectedSerialNumber": "550101046182",
			"connection": map[string]any{"type": "serial", "port": "/dev/terminalfa"},
		}},
	}
	if modify != nil {
		modify(object)
	}
	body, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaultsAndRelativePaths(t *testing.T) {
	path := configFile(t, func(c map[string]any) { c["logging"] = map[string]any{"file": "server.log"} })
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Path != filepath.Join(filepath.Dir(path), "data", "requests.db") || cfg.Logging.File != filepath.Join(filepath.Dir(path), "server.log") {
		t.Fatalf("relative paths were not based on config directory: %+v", cfg)
	}
	if cfg.Devices[0].Connection.BaudRate != 115200 || cfg.Devices[0].Connection.ResponseTimeout.Value() != 25*time.Second || cfg.Devices[0].QueueSize != 32 {
		t.Fatalf("unexpected defaults: %+v", cfg.Devices[0])
	}
}

func TestInvalidConfigurationsFailBeforeHardwareAccess(t *testing.T) {
	cases := map[string]func(map[string]any){
		"unsupported HTTP": func(c map[string]any) {
			c["devices"].([]any)[0].(map[string]any)["connection"].(map[string]any)["type"] = "http"
		},
		"unknown field":    func(c map[string]any) { c["databse"] = "typo" },
		"empty database":   func(c map[string]any) { c["database"] = map[string]any{"path": ""} },
		"memory database":  func(c map[string]any) { c["database"] = map[string]any{"path": ":memory:"} },
		"no devices":       func(c map[string]any) { c["devices"] = []any{} },
		"invalid duration": func(c map[string]any) { c["http"] = map[string]any{"requestTimeout": "forever"} },
		"numeric duration": func(c map[string]any) { c["http"] = map[string]any{"requestTimeout": 30} },
		"negative timeout": func(c map[string]any) { c["http"] = map[string]any{"requestTimeout": "-1s"} },
		"invalid format":   func(c map[string]any) { c["logging"] = map[string]any{"format": "yaml"} },
		"invalid level":    func(c map[string]any) { c["logging"] = map[string]any{"level": "verbose"} },
		"duplicate ID":     func(c map[string]any) { c["devices"] = append(c["devices"].([]any), c["devices"].([]any)[0]) },
		"relative port": func(c map[string]any) {
			c["devices"].([]any)[0].(map[string]any)["connection"].(map[string]any)["port"] = "ttyACM0"
		},
		"bad identity":   func(c map[string]any) { c["devices"].([]any)[0].(map[string]any)["expectedSerialNumber"] = "314" },
		"negative queue": func(c map[string]any) { c["devices"].([]any)[0].(map[string]any)["queueSize"] = -1 },
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(configFile(t, modify))
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func tcpDevice(id, address string) map[string]any {
	return map[string]any{
		"id": id,
		"connection": map[string]any{
			"type": "tcp", "address": address,
		},
	}
}

func TestMixedSerialAndTCPDevices(t *testing.T) {
	path := configFile(t, func(c map[string]any) {
		tcp := tcpDevice("lan-kkt", "192.0.2.50:7778")
		tcp["expectedSerialNumber"] = "550101046183"
		tcp["queueSize"] = 8
		connection := tcp["connection"].(map[string]any)
		connection["connectTimeout"] = "3s"
		connection["responseTimeout"] = "20s"
		c["devices"] = append(c["devices"].([]any), tcp)
	})
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Devices) != 2 {
		t.Fatalf("got %d devices; want 2", len(cfg.Devices))
	}
	serial, tcp := cfg.Devices[0], cfg.Devices[1]
	if serial.Connection.Type != "serial" || serial.Connection.BaudRate != 115200 || serial.Connection.ConnectTimeout != 0 {
		t.Fatalf("serial configuration changed: %+v", serial)
	}
	if tcp.Connection.Type != "tcp" || tcp.Connection.Address != "192.0.2.50:7778" || tcp.Connection.Port != "" || tcp.Connection.BaudRate != 0 || tcp.Connection.ConnectTimeout.Value() != 3*time.Second || tcp.Connection.ResponseTimeout.Value() != 20*time.Second || tcp.QueueSize != 8 {
		t.Fatalf("incorrect TCP configuration: %+v", tcp)
	}
}

func TestTCPDefaultsAndAddressFormsWithoutNetworkAccess(t *testing.T) {
	for _, address := range []string{
		"192.0.2.50:7778",
		"kkt.invalid:7778",
		"KKT.EXAMPLE.:07778",
		"localhost:1",
		"[2001:db8::50]:65535",
		"[::ffff:192.0.2.50]:7778",
		"[fe80::50%eth0]:7778",
	} {
		t.Run(address, func(t *testing.T) {
			path := configFile(t, func(c map[string]any) {
				c["devices"] = []any{tcpDevice("lan-kkt", address)}
			})
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			connection := cfg.Devices[0].Connection
			if connection.Type != "tcp" || connection.Address != address || connection.ConnectTimeout.Value() != 5*time.Second || connection.ResponseTimeout.Value() != 25*time.Second || connection.BaudRate != 0 {
				t.Fatalf("incorrect TCP defaults: %+v", connection)
			}
		})
	}
}

func TestInvalidTCPAddresses(t *testing.T) {
	for _, address := range []string{
		"", "192.0.2.50", ":7778", "192.0.2.50:", "localhost:http",
		"192.0.2.50:0", "192.0.2.50:65536", "192.0.2.50:-1",
		"192.0.2.50:+7778", "192.0.2.50:999999999999999999999999",
		"192.0.2.50:７７７８", "http://192.0.2.50:7778", "https://kkt.example:443",
		"192.0.2.50:7778/path", "user@kkt.example:7778", "kkt.example/path:7778",
		"192.0.2.50:7778?query=1", " 192.0.2.50:7778", "192.0.2.50:7778 ",
		"kkt example:7778", "kkt\texample:7778", "kkt\u00a0example:7778",
		"2001:db8::50:7778", "[2001:db8::50]", "[not-ip]:7778", "[:::]:7778",
		".:7778", "kkt..example:7778", "-kkt.example:7778", "kkt-.example:7778",
	} {
		t.Run(address, func(t *testing.T) {
			path := configFile(t, func(c map[string]any) {
				c["devices"] = []any{tcpDevice("lan-kkt", address)}
			})
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "connection.address") {
				t.Fatalf("expected TCP address validation error for %q, got %v", address, err)
			}
		})
	}
}

func TestDuplicateTCPAddressesAreNormalized(t *testing.T) {
	cases := []struct {
		name          string
		first, second string
	}{
		{"identical IPv4", "192.0.2.50:7778", "192.0.2.50:7778"},
		{"hostname case and FQDN", "KKT.Example.:7778", "kkt.example:7778"},
		{"numeric port", "kkt.example:07778", "kkt.example:7778"},
		{"IPv6 compression", "[2001:0db8:0:0:0:0:0:50]:7778", "[2001:db8::50]:7778"},
		{"IPv4 mapped IPv6", "[::ffff:192.0.2.50]:7778", "192.0.2.50:7778"},
		{"IPv4 mapped hexadecimal", "[::ffff:c000:232]:7778", "192.0.2.50:7778"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := configFile(t, func(c map[string]any) {
				c["devices"] = []any{tcpDevice("first", test.first), tcpDevice("second", test.second)}
			})
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicate TCP address") {
				t.Fatalf("expected duplicate endpoint error, got %v", err)
			}
		})
	}
}

func TestDifferentTCPEndpointsAndUnresolvedAliasesAreAccepted(t *testing.T) {
	path := configFile(t, func(c map[string]any) {
		c["devices"] = []any{
			tcpDevice("first", "192.0.2.50:7778"),
			tcpDevice("other-port", "192.0.2.50:7779"),
			tcpDevice("hostname-alias", "kkt.invalid:7778"),
		}
	})
	if _, err := Load(path); err != nil {
		t.Fatalf("distinct lexical endpoints should be accepted without DNS: %v", err)
	}
}

func TestTransportFieldsAndTimeouts(t *testing.T) {
	cases := []struct {
		name       string
		connection map[string]any
	}{
		{"TCP serial path", map[string]any{"type": "tcp", "address": "192.0.2.50:7778", "port": "/dev/terminalfa"}},
		{"TCP baud rate", map[string]any{"type": "tcp", "address": "192.0.2.50:7778", "baudRate": 115200}},
		{"TCP negative baud rate", map[string]any{"type": "tcp", "address": "192.0.2.50:7778", "baudRate": -1}},
		{"serial TCP address", map[string]any{"port": "/dev/terminalfa", "address": "192.0.2.50:7778"}},
		{"serial connect timeout", map[string]any{"port": "/dev/terminalfa", "connectTimeout": "1s"}},
		{"serial negative connect timeout", map[string]any{"port": "/dev/terminalfa", "connectTimeout": "-1s"}},
		{"serial no port", map[string]any{"type": "serial"}},
		{"serial negative baud rate", map[string]any{"port": "/dev/terminalfa", "baudRate": -1}},
		{"TCP no address", map[string]any{"type": "tcp"}},
		{"TCP negative connect timeout", map[string]any{"type": "tcp", "address": "192.0.2.50:7778", "connectTimeout": "-1s"}},
		{"TCP excessive connect timeout", map[string]any{"type": "tcp", "address": "192.0.2.50:7778", "connectTimeout": "5m1s"}},
		{"TCP negative response timeout", map[string]any{"type": "tcp", "address": "192.0.2.50:7778", "responseTimeout": "-1s"}},
		{"TCP excessive response timeout", map[string]any{"type": "tcp", "address": "192.0.2.50:7778", "responseTimeout": "5m1s"}},
		{"serial negative response timeout", map[string]any{"port": "/dev/terminalfa", "responseTimeout": "-1s"}},
		{"serial excessive response timeout", map[string]any{"port": "/dev/terminalfa", "responseTimeout": "5m1s"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := configFile(t, func(c map[string]any) {
				c["devices"] = []any{map[string]any{"id": "kkt-1", "connection": test.connection}}
			})
			if _, err := Load(path); err == nil {
				t.Fatalf("expected validation error for %+v", test.connection)
			}
		})
	}
}

func TestLegacySerialConfigurationAndTimeoutBounds(t *testing.T) {
	path := configFile(t, func(c map[string]any) {
		connection := c["devices"].([]any)[0].(map[string]any)["connection"].(map[string]any)
		delete(connection, "type")
	})
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	connection := cfg.Devices[0].Connection
	if connection.Type != "serial" || connection.Port != "/dev/terminalfa" || connection.BaudRate != 115200 || connection.ResponseTimeout.Value() != 25*time.Second || connection.ConnectTimeout != 0 {
		t.Fatalf("legacy serial defaults changed: %+v", connection)
	}
	path = configFile(t, func(c map[string]any) {
		tcp := tcpDevice("tcp", "192.0.2.50:7778")
		tcp["connection"].(map[string]any)["connectTimeout"] = "5m"
		tcp["connection"].(map[string]any)["responseTimeout"] = "5m"
		c["devices"] = []any{tcp}
	})
	if _, err := Load(path); err != nil {
		t.Fatalf("inclusive timeout maximum rejected: %v", err)
	}
}

func TestDuplicateIdentityAcrossTransportsRejected(t *testing.T) {
	path := configFile(t, func(c map[string]any) {
		tcp := tcpDevice("lan-kkt", "192.0.2.50:7778")
		tcp["expectedSerialNumber"] = "550101046182"
		c["devices"] = append(c["devices"].([]any), tcp)
	})
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicate expectedSerialNumber") {
		t.Fatalf("expected duplicate identity rejection across transports, got %v", err)
	}
}

func TestUnknownTransportExplainsTCPIsNotHTTP(t *testing.T) {
	path := configFile(t, func(c map[string]any) {
		c["devices"] = []any{map[string]any{"id": "kkt-1", "connection": map[string]any{"type": "http"}}}
	})
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "binary TCP, not HTTP") {
		t.Fatalf("expected unknown transport explanation, got %v", err)
	}
}

func TestDuplicatePortThroughSymlinkRejected(t *testing.T) {
	path := configFile(t, nil)
	port := filepath.Join(filepath.Dir(path), "port")
	alias := filepath.Join(filepath.Dir(path), "alias")
	if err := os.WriteFile(port, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(port, alias); err != nil {
		t.Fatal(err)
	}
	body := `{"devices":[{"id":"one","connection":{"port":"` + port + `"}},{"id":"two","connection":{"port":"` + alias + `"}}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicate serial port") {
		t.Fatalf("expected duplicate port error, got %v", err)
	}
}

func TestTrailingConfigurationRejected(t *testing.T) {
	path := configFile(t, nil)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(` {}`)
	_ = f.Close()
	if _, err := Load(path); err == nil {
		t.Fatal("accepted multiple JSON values")
	}
}
