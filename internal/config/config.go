// Package config loads and validates the JSON server configuration.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sirupsen/logrus"
)

type Duration time.Duration

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("duration must be a string such as 25s: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Value() time.Duration { return time.Duration(d) }

type Config struct {
	HTTP     HTTP     `json:"http"`
	Logging  Logging  `json:"logging"`
	Database Database `json:"database"`
	Devices  []Device `json:"devices"`
}

type HTTP struct {
	Listen            string   `json:"listen"`
	ReadHeaderTimeout Duration `json:"readHeaderTimeout"`
	RequestTimeout    Duration `json:"requestTimeout"`
	ShutdownTimeout   Duration `json:"shutdownTimeout"`
	MaxBodyBytes      int64    `json:"maxBodyBytes"`
	BearerToken       string   `json:"bearerToken"`
}

type Logging struct {
	Level  string `json:"level"`
	Format string `json:"format"`
	File   string `json:"file"`
}

type Database struct {
	Path string `json:"path"`
}

type Device struct {
	ID                   string     `json:"id"`
	ProtocolProfile      string     `json:"protocolProfile,omitempty"`
	ExpectedSerialNumber string     `json:"expectedSerialNumber"`
	QueueSize            int        `json:"queueSize"`
	Connection           Connection `json:"connection"`
}

type Connection struct {
	Type            string   `json:"type"`
	Port            string   `json:"port"`
	BaudRate        int      `json:"baudRate"`
	ResponseTimeout Duration `json:"responseTimeout"`
	Address         string   `json:"address,omitempty"`
	ConnectTimeout  Duration `json:"connectTimeout,omitempty"`
}

var ValidID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var factorySerial = regexp.MustCompile(`^[0-9]{12}$`)

func Load(path string) (Config, error) {
	c := Config{
		HTTP: HTTP{
			Listen: "127.0.0.1:8080", ReadHeaderTimeout: Duration(5 * time.Second),
			RequestTimeout: Duration(30 * time.Second), ShutdownTimeout: Duration(65 * time.Second),
			MaxBodyBytes: 65536,
		},
		Logging:  Logging{Level: "info", Format: "json"},
		Database: Database{Path: "data/requests.db"},
	}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 1024*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("decode configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("configuration must contain one JSON object")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return c, err
	}
	if strings.TrimSpace(c.Database.Path) == "" || c.Database.Path == ":memory:" {
		return c, fmt.Errorf("database.path must be a persistent file path")
	}
	if !filepath.IsAbs(c.Database.Path) {
		c.Database.Path = filepath.Join(base, c.Database.Path)
	}
	if c.Logging.File != "" && !filepath.IsAbs(c.Logging.File) {
		c.Logging.File = filepath.Join(base, c.Logging.File)
	}
	return c, c.Validate()
}

func (c *Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.HTTP.Listen); err != nil {
		return fmt.Errorf("http.listen must be host:port: %w", err)
	}
	if c.HTTP.ReadHeaderTimeout <= 0 || c.HTTP.RequestTimeout <= 0 || c.HTTP.ShutdownTimeout <= 0 {
		return fmt.Errorf("HTTP timeouts must be positive")
	}
	if c.HTTP.MaxBodyBytes < 1 || c.HTTP.MaxBodyBytes > 1024*1024 {
		return fmt.Errorf("http.maxBodyBytes must be between 1 and 1048576")
	}
	if _, err := logrus.ParseLevel(c.Logging.Level); err != nil {
		return fmt.Errorf("logging.level: %w", err)
	}
	if c.Logging.Format != "json" && c.Logging.Format != "text" {
		return fmt.Errorf("logging.format must be json or text")
	}
	if c.Database.Path == "" || c.Database.Path == ":memory:" {
		return fmt.Errorf("database.path must be a persistent file path")
	}
	if len(c.Devices) == 0 {
		return fmt.Errorf("at least one device must be configured")
	}
	ids := make(map[string]bool)
	ports := make(map[string]bool)
	addresses := make(map[string]bool)
	serials := make(map[string]bool)
	for i := range c.Devices {
		d := &c.Devices[i]
		if !ValidID.MatchString(d.ID) || ids[d.ID] {
			return fmt.Errorf("device %d has an invalid or duplicate id %q", i, d.ID)
		}
		ids[d.ID] = true
		if d.ProtocolProfile == "" {
			d.ProtocolProfile = "base"
		}
		if d.ProtocolProfile != "base" && d.ProtocolProfile != "ffd1.2" {
			return fmt.Errorf("device %s protocolProfile must be base or ffd1.2", d.ID)
		}
		if d.ExpectedSerialNumber != "" {
			if !factorySerial.MatchString(d.ExpectedSerialNumber) || serials[d.ExpectedSerialNumber] {
				return fmt.Errorf("device %s has an invalid or duplicate expectedSerialNumber", d.ID)
			}
			serials[d.ExpectedSerialNumber] = true
		}
		if d.QueueSize == 0 {
			d.QueueSize = 32
		}
		if d.QueueSize < 1 || d.QueueSize > 10000 {
			return fmt.Errorf("device %s queueSize must be between 1 and 10000", d.ID)
		}
		if d.Connection.Type == "" {
			d.Connection.Type = "serial"
		}
		switch d.Connection.Type {
		case "serial":
			if !filepath.IsAbs(d.Connection.Port) || d.Connection.Address != "" || d.Connection.ConnectTimeout != 0 {
				return fmt.Errorf("device %s requires an absolute serial port path and no TCP address or connectTimeout", d.ID)
			}
			port := filepath.Clean(d.Connection.Port)
			if real, err := filepath.EvalSymlinks(port); err == nil {
				port = real
			}
			if ports[port] {
				return fmt.Errorf("duplicate serial port %q", d.Connection.Port)
			}
			ports[port] = true
			if d.Connection.BaudRate == 0 {
				d.Connection.BaudRate = 115200
			}
			if d.Connection.BaudRate <= 0 {
				return fmt.Errorf("device %s baudRate must be positive", d.ID)
			}
		case "tcp":
			if d.Connection.Port != "" || d.Connection.BaudRate != 0 {
				return fmt.Errorf("device %s TCP connection must not contain serial port or baudRate", d.ID)
			}
			address, err := canonicalTCPAddress(d.Connection.Address)
			if err != nil {
				return fmt.Errorf("device %s connection.address: %w", d.ID, err)
			}
			if addresses[address] {
				return fmt.Errorf("duplicate TCP address %q", d.Connection.Address)
			}
			addresses[address] = true
			if d.Connection.ConnectTimeout == 0 {
				d.Connection.ConnectTimeout = Duration(5 * time.Second)
			}
			if d.Connection.ConnectTimeout <= 0 || d.Connection.ConnectTimeout > Duration(5*time.Minute) {
				return fmt.Errorf("device %s connectTimeout must be positive and at most 5m", d.ID)
			}
		default:
			return fmt.Errorf("device %s: unsupported connection type %q; use serial or tcp (the device uses binary TCP, not HTTP)", d.ID, d.Connection.Type)
		}
		if d.Connection.ResponseTimeout == 0 {
			d.Connection.ResponseTimeout = Duration(25 * time.Second)
		}
		if d.Connection.ResponseTimeout <= 0 || d.Connection.ResponseTimeout > Duration(5*time.Minute) {
			return fmt.Errorf("device %s responseTimeout must be positive and at most 5m", d.ID)
		}
	}
	return nil
}

// canonicalTCPAddress validates syntax without resolving names or connecting to
// hardware. Hostname/IP aliases cannot be detected without network access.
func canonicalTCPAddress(address string) (string, error) {
	if strings.Contains(address, "://") {
		return "", fmt.Errorf("must be host:numeric-port for binary TCP, not an HTTP URL")
	}
	if strings.IndexFunc(address, unicode.IsSpace) != -1 {
		return "", fmt.Errorf("must not contain whitespace")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("must be host:numeric-port, using brackets around IPv6 addresses")
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return "", fmt.Errorf("TCP port must be numeric and between 1 and 65535")
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("TCP port must be numeric and between 1 and 65535")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if strings.HasPrefix(address, "[") && !ip.Is6() {
			return "", fmt.Errorf("brackets must contain an IPv6 address")
		}
		host = ip.Unmap().String()
	} else {
		if strings.HasPrefix(address, "[") {
			return "", fmt.Errorf("brackets must contain an IPv6 address")
		}
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if len(host) == 0 || len(host) > 253 {
			return "", fmt.Errorf("host must be a hostname or IP address")
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", fmt.Errorf("host must be a hostname or IP address")
			}
			for _, char := range label {
				if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
					return "", fmt.Errorf("host must be a hostname or IP address")
				}
			}
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(portNumber)), nil
}
