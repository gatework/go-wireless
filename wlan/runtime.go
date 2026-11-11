// Package wlan is the typed wireless-controller domain model: the parsed
// configuration and runtime tables of one controller (AC).
//
// Field names and JSON tags match the H3C and Huawei parsers, so parsed
// observations decode without loss. NewController rejects fields the model
// does not know: the dimension becomes partial with an unmodeled_fact issue
// instead of silently losing facts.
package wlan

import "encoding/json"

// Number is an exact device integer (counts, durations) that may exceed int64.
type Number = json.Number

// Details is a pass-through bag of recognized secondary labels, kept exact.
type Details = map[string]json.RawMessage

// AccessPoint is one AP of "display wlan ap all verbose" or "display ap all".
type AccessPoint struct {
	Name              string  `json:"name,omitempty"`
	APID              string  `json:"ap_id,omitempty"`
	Model             string  `json:"model,omitempty"`
	Serial            string  `json:"serial,omitempty"`
	GroupName         string  `json:"group_name,omitempty"`
	MACAddress        string  `json:"mac_address,omitempty"`
	ManagementIP      *string `json:"management_ip,omitempty"`
	OperationalStatus string  `json:"operational_status,omitempty"`
	OSVersion         string  `json:"os_version,omitempty"`
	ClientCount       *Number `json:"client_count,omitempty"`
	OnlineSeconds     *Number `json:"online_seconds,omitempty"`
	UptimeSeconds     *Number `json:"uptime_seconds,omitempty"`
	Details           Details `json:"details,omitempty"`
}

// Radio is one AP radio.
type Radio struct {
	APName       string   `json:"ap_name,omitempty"`
	RadioID      string   `json:"radio_id,omitempty"`
	AdminStatus  string   `json:"admin_status,omitempty"`
	Protocol     string   `json:"protocol,omitempty"`
	Channel      *int     `json:"channel,omitempty"`
	ChannelWidth *Number  `json:"channel_width,omitempty"`
	NoiseDBm     *float64 `json:"noise_dbm,omitempty"`
	Utilization  *int     `json:"utilization,omitempty"` // percent
	Details      Details  `json:"details,omitempty"`
}

// BSS is one basic service set.
type BSS struct {
	APID              string  `json:"ap_id,omitempty"`
	APName            string  `json:"ap_name,omitempty"`
	RadioID           string  `json:"radio_id,omitempty"`
	BSSID             string  `json:"bssid,omitempty"`
	SSID              string  `json:"ssid,omitempty"`
	Profile           string  `json:"profile,omitempty"`
	OperationalStatus string  `json:"operational_status,omitempty"`
	ClientCount       *Number `json:"client_count,omitempty"`
	VLAN              *int    `json:"vlan,omitempty"`
	ServiceVLAN       *int    `json:"service_vlan,omitempty"`
	Security          Details `json:"security,omitempty"`
	Details           Details `json:"details,omitempty"`
}

// Client is one associated station.
type Client struct {
	ClientMAC         string   `json:"client_mac,omitempty"`
	ClientIP          *string  `json:"client_ip,omitempty"`
	ClientIPv6        *string  `json:"client_ipv6,omitempty"`
	Username          string   `json:"username,omitempty"`
	APID              string   `json:"ap_id,omitempty"`
	APName            string   `json:"ap_name,omitempty"`
	RadioID           string   `json:"radio_id,omitempty"`
	BSSID             string   `json:"bssid,omitempty"`
	SSID              string   `json:"ssid,omitempty"`
	Band              string   `json:"band,omitempty"`
	Channel           *int     `json:"channel,omitempty"`
	ChannelWidth      *Number  `json:"channel_width,omitempty"`
	Protocol          string   `json:"protocol,omitempty"`
	AKM               string   `json:"akm,omitempty"`
	Cipher            string   `json:"cipher,omitempty"`
	Security          string   `json:"security,omitempty"`
	AuthMethod        string   `json:"auth_method,omitempty"`
	QoS               string   `json:"qos,omitempty"`
	PowerSave         string   `json:"power_save,omitempty"`
	PowerSaveMode     string   `json:"power_save_mode,omitempty"`
	OperationalStatus string   `json:"operational_status,omitempty"`
	VLAN              *int     `json:"vlan,omitempty"`
	ServiceVLAN       *int     `json:"service_vlan,omitempty"`
	RSSI              *Number  `json:"rssi,omitempty"`
	SignalDBm         *float64 `json:"signal_dbm,omitempty"`
	RxRateMbps        *float64 `json:"rx_rate_mbps,omitempty"`
	TxRateMbps        *float64 `json:"tx_rate_mbps,omitempty"`
	OnlineSeconds     *Number  `json:"online_seconds,omitempty"`
	UptimeSeconds     *Number  `json:"uptime_seconds,omitempty"`
	Details           Details  `json:"details,omitempty"`
}

// AuthSession is one 802.1X or MAC-authentication connection.
type AuthSession struct {
	MACAddress       string  `json:"mac_address,omitempty"`
	Username         string  `json:"username,omitempty"`
	Domain           string  `json:"domain,omitempty"`
	State            string  `json:"state,omitempty"`
	Protocol         string  `json:"protocol,omitempty"`
	Interface        string  `json:"interface,omitempty"`
	IPAddress        *string `json:"ip_address,omitempty"`
	IPv6Address      *string `json:"ipv6_address,omitempty"`
	APID             string  `json:"ap_id,omitempty"`
	APName           string  `json:"ap_name,omitempty"`
	RadioID          string  `json:"radio_id,omitempty"`
	BSSID            string  `json:"bssid,omitempty"`
	SSID             string  `json:"ssid,omitempty"`
	InitialVLANID    *int    `json:"initial_vlan_id,omitempty"`
	AuthorizedVLANID *int    `json:"authorized_vlan_id,omitempty"`
	OnlineSeconds    *Number `json:"online_seconds,omitempty"`
	Details          Details `json:"details,omitempty"`
}

// BlockedClient is one client-security block-mac entry.
type BlockedClient struct {
	MACAddress string `json:"mac_address"`
	Reason     string `json:"reason,omitempty"`
}

// IPConflict is one historical client IP conflict event.
type IPConflict struct {
	IPAddress     string `json:"ip_address"`
	OldMACAddress string `json:"old_mac_address,omitempty"`
	OldAPID       string `json:"old_ap_id,omitempty"`
	NewMACAddress string `json:"new_mac_address,omitempty"`
	NewAPID       string `json:"new_ap_id,omitempty"`
	OccurredAt    string `json:"occurred_at,omitempty"` // without year on H3C
}

// ModelRadio is one radio of an AP model.
type ModelRadio struct {
	RadioID     string   `json:"radio_id,omitempty"`
	BSSCount    *Number  `json:"bss_count,omitempty"`
	DefaultMode string   `json:"default_mode,omitempty"`
	Modes       []string `json:"modes,omitempty"`
}

// ModelVersion is one hardware/software pairing of an AP model.
type ModelVersion struct {
	HardwareVersion        string `json:"hardware_version,omitempty"`
	SoftwareVersion        string `json:"software_version,omitempty"`
	DefaultSoftwareVersion string `json:"default_software_version,omitempty"`
	ImageName              string `json:"image_name,omitempty"`
}

// APModel is one row of "display wlan ap-model all".
type APModel struct {
	Model         string         `json:"model"`
	Alias         string         `json:"alias,omitempty"`
	Type          string         `json:"type,omitempty"`
	VendorID      *Number        `json:"vendor_id,omitempty"`
	VendorName    string         `json:"vendor_name,omitempty"`
	RadioCount    *Number        `json:"radio_count,omitempty"`
	LicenseType   *Number        `json:"license_type,omitempty"`
	LicenseWeight *Number        `json:"license_weight,omitempty"`
	Radios        []ModelRadio   `json:"radios,omitempty"`
	Versions      []ModelVersion `json:"versions,omitempty"`
	Details       Details        `json:"details,omitempty"`
}

// LLDPNeighbor is an AP uplink neighbor learned by logging into the AP.
type LLDPNeighbor struct {
	APName            string  `json:"ap_name,omitempty"`
	LocalInterface    string  `json:"local_interface,omitempty"`
	RemoteChassisID   string  `json:"remote_chassis_id,omitempty"`
	RemoteName        string  `json:"remote_name,omitempty"`
	RemoteInterface   string  `json:"remote_interface,omitempty"`
	ManagementAddress string  `json:"management_address,omitempty"`
	Details           Details `json:"details,omitempty"`
}
