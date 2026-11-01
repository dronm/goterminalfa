package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// settingsCommands contains the documented settings after the original 0x73
// boundary. All layouts are named TLVs, independent of the server transport.
func settingsCommands() []Command {
	networkFields := networkSettingsFields()
	networkOutput := append([]ParameterDefinition(nil), networkFields...)
	for i := 1; i < len(networkOutput); i++ {
		networkOutput[i].MinLength = 0
	}
	ofdInput := ofdSettingsFields()
	ofdOutput := append([]ParameterDefinition(nil), ofdInput...)
	ofdOutput[0].MinLength = 0
	ofdOutput[0].Description = "Configured OFD domain name; the returned value may be empty when not configured."
	// The fallback address is explicitly optional when a domain is configured.
	// An unset value may be represented by omission or an empty TLV on reads.
	ofdOutput[1].MinLength = 0
	printerOutput := printerSettingsFields()
	printerInput := append([]ParameterDefinition(nil), printerOutput...)
	printerInput[0].Required = false
	printerInput[0].Notes += " This parameter is explicitly optional in command 0x78."
	controlFields := controlSettingsFields()
	return []Command{
		makeCommand(commandSpec{
			Code: 0x74, Name: "setNetworkSettings", TitleRU: "Установить сетевые настройки ККТ", Category: "settings", Page: 29, RequestLayout: "tlv",
			Description:   "Set the KKT network settings. All four documented fields are required; dynamic addressing is selected explicitly with 0 or 1. IPv4 addresses use textual CP866 values, not binary IPv4 bytes.",
			DescriptionRU: "Установить сетевые настройки ККТ. Использовать динамический адрес: TLV, TAG=30001, LEN=1, BYTE, 0 — статический адрес, 1 — динамический адрес. IP-адрес: TAG=30002, маска подсети: TAG=30003, шлюз по умолчанию: TAG=30004; каждый имеет тип ASCII, длина не более 15 символов. Выходные данные: нет.",
			Input:         networkFields, Validate: validateNetworkSettings,
			Notes: []string{"The table does not document omitted network fields, patch semantics, or defaults. All four fields must be supplied even when dynamicAddress is 1; 0.0.0.0 is accepted as an IPv4 placeholder.", "Changing KKT Ethernet settings does not update the server's configured device address or transport. On a TCP-connected device, this must be the final step of an operation because the endpoint may change."},
		}),
		makeCommand(commandSpec{
			Code: 0x75, Name: "getNetworkSettings", TitleRU: "Запрос сетевых настроек из ККТ", Category: "settings", ReadOnly: true, Page: 29, ResponseLayout: "tlv",
			Description:   "Read the KKT network settings as TLVs: dynamic addressing flag, textual IPv4 address, subnet mask, and default gateway. Unknown future tags are preserved.",
			DescriptionRU: "Запрос сетевых настроек из ККТ. Входные параметры: нет. Выходные данные: использовать динамический адрес, TAG=30001, BYTE; IP-адрес, TAG=30002; маска подсети, TAG=30003; шлюз по умолчанию, TAG=30004. Адресные поля имеют тип ASCII и длину не более 15 символов. Значение динамического адреса: 0 — статический, 1 — динамический.",
			Output:        networkOutput,
			Notes:         []string{"Address values are returned as stored, including empty values from an unconfigured device. IPv4 and subnet-mask semantic validation applies to setter inputs, not to reading the current state."},
		}),
		makeCommand(commandSpec{
			Code: 0x76, Name: "setOfdSettings", TitleRU: "Установить настройки ОФД в ККТ", Category: "settings", Page: 30, RequestLayout: "tlv",
			Description:   "Set the OFD domain, optional fallback IPv4 address, TCP port, and interaction timer in seconds. The domain is required; an omitted fallback address remains absent from the wire payload.",
			DescriptionRU: "Установить настройки ОФД в ККТ. Доменное имя ОФД: TLV, TAG=30040, ASCII, не более 128 символов, обязательный параметр. IP-адрес ОФД: TAG=30005, ASCII, не более 15 символов; его можно не указывать. TCP-порт: TAG=30006, LEN=2, UINT16 LE. Интервал таймера ОФД в секундах: TAG=30009, LEN=2, UINT16 LE, таймер потока взаимодействия с ОФД. Если IP-адрес указан, при неуспешном разрешении доменного имени обмен осуществляется по указанному IP. Адрес DNS-сервера определяется автоматически при DHCP, иначе по внутреннему списку DNS-серверов ККТ. Выходные данные: нет.",
			Input:         ofdInput, Validate: validateOfdSettings,
			Notes: []string{"Only the fallback IPv4 address is documented as optional. The driver does not invent defaults for the TCP port or timer and preserves explicit zero values within UINT16.", "The fallback IPv4 address is omitted by leaving ipAddress out of params; null or an empty input address is not an omission."},
		}),
		makeCommand(commandSpec{
			Code: 0x77, Name: "getOfdSettings", TitleRU: "Запрос настроек ОФД из ККТ", Category: "settings", ReadOnly: true, Page: 29, ResponseLayout: "tlv",
			Description:   "Read the OFD domain, optional fallback IPv4 address, TCP port, and interaction timer. UINT16 values are decoded little-endian, and unknown TLV tags are retained.",
			DescriptionRU: "Запрос настроек ОФД из ККТ. Входные параметры: нет. Выходные данные: доменное имя ОФД, TAG=30040, ASCII, не более 128 символов; IP-адрес ОФД, TAG=30005, ASCII, не более 15 символов; TCP-порт, TAG=30006, LEN=2, UINT16 LE; интервал таймера ОФД в секундах, TAG=30009, LEN=2, UINT16 LE. Таблица продолжается на странице 30.",
			Output:        ofdOutput,
			Notes:         []string{"An unset optional fallback IPv4 address may be absent or returned as an empty string. The domain tag may contain an empty unconfigured value; the domain, port, and timer remain documented response fields. Getter values are returned as stored rather than subjected to setter semantic validation."},
		}),
		makeCommand(commandSpec{
			Code: 0x78, Name: "setPrinterSettings", TitleRU: "Установить настройки печати в ККТ", Category: "settings", Page: 30, RequestLayout: "tlv",
			Description:   "Set printer font size, retract behavior, and additional receipt text. The printer model is optional. Text is limited to 128 bytes after CP866 encoding.",
			DescriptionRU: "Установить настройки печати в ККТ. Модель печатающего устройства: TLV, TAG=30019, LEN=1, BYTE, необязательный параметр. Размер шрифта: TAG=30015, LEN=1, BYTE, 0 — обычный шрифт, 1 — уменьшенный. Выполнять Retract для отчетов открытия и закрытия смены: TAG=30018, LEN=1, BYTE, 1 — забирать документ внутрь, 0 — выталкивать наружу; для печатающих устройств, поддерживающих retract. Дополнительный текст на кассовом чеке: TAG=30017, ASCII, не более 128 символов, печатается после QR-кода. Выходные данные: нет.",
			Input:         printerInput,
			Notes:         printerSettingsNotes(),
		}),
		makeCommand(commandSpec{
			Code: 0x79, Name: "getPrinterSettings", TitleRU: "Запрос настроек печати из ККТ", Category: "settings", ReadOnly: true, Page: 30, ResponseLayout: "tlv",
			Description:   "Read the printer model, font size, retract behavior, and extra receipt text. The model dictionary includes the explicitly documented values 0 through 10.",
			DescriptionRU: "Запрос настроек печати из ККТ. Входные параметры: нет. Выходные данные: модель печатающего устройства, TAG=30019, LEN=1, BYTE; размер шрифта, TAG=30015, LEN=1, BYTE, 0 — обычный, 1 — уменьшенный; Retract для отчетов открытия и закрытия смены, TAG=30018, LEN=1, BYTE, 1 — забирать внутрь, 0 — выталкивать наружу; дополнительный текст на кассовом чеке, TAG=30017, ASCII, не более 128 символов, печатается после QR-кода.",
			Output:        printerOutput,
			Notes:         printerSettingsNotes(),
		}),
		makeCommand(commandSpec{
			Code: 0x80, Name: "setControlSettings", TitleRU: "Установить параметры управления ККТ", Category: "settings", Page: 31, RequestLayout: "tlv",
			Description:   "Set the KKT control interface and command TCP port. Interface 0 selects serial and 1 selects Ethernet. This changes the device settings, not this server's transport configuration.",
			DescriptionRU: "Установить параметры управления ККТ. Интерфейс управления: TLV, TAG=30030, LEN=1, BYTE, 0 — RS232, 1 — Ethernet TCP/IP. Номер TCP-порта: TLV, TAG=30032, LEN=2, UINT16 LE. Выходные данные: нет.",
			Input:         controlFields,
			Notes:         controlSettingsNotes(),
		}),
		makeCommand(commandSpec{
			Code: 0x81, Name: "getControlSettings", TitleRU: "Запрос параметров управления ККТ", Category: "settings", ReadOnly: true, Page: 31, ResponseLayout: "tlv",
			Description:   "Read the selected KKT control interface and command TCP port. Interface 0 means serial, and 1 means Ethernet TCP/IP.",
			DescriptionRU: "Запрос параметров управления ККТ. Входные параметры: нет. Выходные данные: интерфейс управления, TAG=30030, LEN=1, BYTE, 0 — RS232, 1 — Ethernet TCP/IP; номер TCP-порта, TAG=30032, LEN=2, UINT16 LE.",
			Output:        controlFields,
			Notes:         controlSettingsNotes(),
		}),
	}
}

func networkSettingsFields() []ParameterDefinition {
	return []ParameterDefinition{
		{Name: "dynamicAddress", TitleRU: "Использовать динамический адрес", Description: "0 for a static address, 1 for dynamic addressing.", JSONType: "integer", WireType: "BYTE", Tag: 30001, Required: true, Enum: map[string]string{"0": "Static", "1": "Dynamic"}},
		{Name: "ipAddress", TitleRU: "IP-адрес", Description: "KKT IPv4 address in dotted-decimal text.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 30002, Required: true, MinLength: 1, MaxLength: 15},
		{Name: "subnetMask", TitleRU: "Маска подсети", Description: "Contiguous IPv4 subnet mask in dotted-decimal text.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 30003, Required: true, MinLength: 1, MaxLength: 15},
		{Name: "gateway", TitleRU: "Шлюз по умолчанию", Description: "Default IPv4 gateway in dotted-decimal text.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 30004, Required: true, MinLength: 1, MaxLength: 15},
	}
}

func ofdSettingsFields() []ParameterDefinition {
	return []ParameterDefinition{
		{Name: "domain", TitleRU: "Доменное имя ОФД", Description: "Required OFD domain name; at most 128 CP866 bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 30040, Required: true, MinLength: 1, MaxLength: 128},
		{Name: "ipAddress", TitleRU: "IP-адрес ОФД", Description: "Optional fallback IPv4 address used if domain resolution fails.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 30005, MinLength: 1, MaxLength: 15},
		{Name: "tcpPort", TitleRU: "TCP-порт", Description: "OFD connection TCP port, encoded as an unsigned 16-bit little-endian integer.", JSONType: "integer", WireType: "UINT16", Tag: 30006, Required: true},
		{Name: "timerSeconds", TitleRU: "Интервал таймера ОФД (сек)", Description: "OFD interaction timer interval in seconds; no default is imposed by the driver.", JSONType: "integer", WireType: "UINT16", Tag: 30009, Required: true, Unit: "seconds"},
	}
}

func printerSettingsFields() []ParameterDefinition {
	return []ParameterDefinition{
		{Name: "printerModel", TitleRU: "Модель печатающего устройства", Description: "Printer model dictionary from Appendix 4, page 36.", JSONType: "integer", WireType: "BYTE", Tag: 30019, Required: true, Enum: map[string]string{"0": "Automatic detection", "1": "Custom-VKP80", "2": "Custom-TG2480H", "3": "Citizen-CTS2000", "4": "Citizen-PPU700", "5": "Star TSP (ESC/POS)", "6": "Star TUP (ESC/POS)", "7": "Epson EU-422", "8": "Custom-TG02H", "9": "DP_Q582P", "10": "Custom-TG2460H"}, Notes: "The settings table says 0–7, but the explicit printer dictionary on page 36 extends the documented values through 10."},
		{Name: "fontSize", TitleRU: "Размер шрифта", Description: "0 for normal font, 1 for reduced font.", JSONType: "integer", WireType: "BYTE", Tag: 30015, Required: true, Enum: map[string]string{"0": "Normal", "1": "Reduced"}},
		{Name: "retract", TitleRU: "Выполнять Retract для отчетов открытия/закрытия смены", Description: "0 ejects the report; 1 retracts it on printers supporting retract.", JSONType: "integer", WireType: "BYTE", Tag: 30018, Required: true, Enum: map[string]string{"0": "Eject", "1": "Retract"}},
		{Name: "additionalText", TitleRU: "Дополнительный текст на кассовом чеке", Description: "Additional receipt text printed after the QR code; an empty string is encoded explicitly.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 30017, Required: true, MaxLength: 128},
	}
}

func controlSettingsFields() []ParameterDefinition {
	return []ParameterDefinition{
		{Name: "controlInterface", TitleRU: "Интерфейс управления", Description: "0 selects the serial interface; 1 selects Ethernet TCP/IP.", JSONType: "integer", WireType: "BYTE", Tag: 30030, Required: true, Enum: map[string]string{"0": "Serial (RS232)", "1": "Ethernet (TCP/IP)"}},
		{Name: "tcpPort", TitleRU: "Номер TCP-порта", Description: "KKT command-server TCP port as an unsigned 16-bit little-endian integer.", JSONType: "integer", WireType: "UINT16", Tag: 30032, Required: true},
	}
}

func printerSettingsNotes() []string {
	return []string{"The settings table lists printer models 0–7; the explicit dictionary in Appendix 4, page 36, also documents models 8, 9, and 10, so all values 0–10 are accepted.", "Only printerModel is marked optional in the set command. fontSize, retract, and additionalText must be supplied; omission is not treated as a patch or an instruction to clear a setting.", "Retract behavior applies only to printers supporting that function. Additional text is printed after the receipt QR code."}
}

func controlSettingsNotes() []string {
	return []string{"Changing the KKT control interface or TCP command port may interrupt the active connection. If its response is lost, the request remains an unknown outcome and is not automatically retried.", "A setControlSettings step selecting Ethernet must be the final step of an executeOperation batch. On a TCP-connected device, any setControlSettings step must be final because its interface or port can change.", "Writing KKT control settings does not rewrite the server's per-device address or connection type. Configure this server's serial or TCP transport separately and use a static IP address or DHCP reservation for LAN devices."}
}

func validateNetworkSettings(params map[string]json.RawMessage) error {
	return validateSettingsAddresses(params, []string{"ipAddress", "subnetMask", "gateway"}, false)
}

func validateOfdSettings(params map[string]json.RawMessage) error {
	var domain string
	if err := json.Unmarshal(params["domain"], &domain); err != nil || strings.TrimSpace(domain) == "" {
		return fmt.Errorf("domain must be a nonempty string")
	}
	return validateSettingsAddresses(params, []string{"ipAddress"}, false)
}

func validateSettingsAddresses(params map[string]json.RawMessage, fields []string, allowEmpty bool) error {
	for _, name := range fields {
		raw, present := params[name]
		if !present {
			continue
		}
		var address string
		if err := json.Unmarshal(raw, &address); err != nil {
			return fmt.Errorf("%s must be a string containing an IPv4 address", name)
		}
		if address == "" && allowEmpty {
			continue
		}
		if err := validateSettingsIPv4(name, address); err != nil {
			return err
		}
	}
	return nil
}

func validateSettingsIPv4(name, value string) error {
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() {
		return fmt.Errorf("%s must be a dotted-decimal IPv4 address", name)
	}
	if name == "subnetMask" {
		_, bits := net.IPMask(address.AsSlice()).Size()
		if bits != 32 {
			return fmt.Errorf("subnetMask must be a contiguous IPv4 mask")
		}
	}
	return nil
}
