package wlan

// Configuration is the parsed "display current-configuration" of an AC.
type Configuration struct {
	GlobalSettings   Settings            `json:"global_settings"`
	ServiceTemplates []ServiceTemplate   `json:"service_templates"`
	APGroups         []APGroup           `json:"ap_groups"`
	AccessPoints     map[string]Settings `json:"access_points"` // AP name -> AP-level settings
	Blacklist        []MACListEntry      `json:"blacklist,omitempty"`
	Whitelist        []MACListEntry      `json:"whitelist,omitempty"`
}

// MACListEntry is one static client black/white list entry.
type MACListEntry struct {
	MACAddress string  `json:"mac_address"`
	Reason     *string `json:"reason"`
}

// ServiceTemplate is a WLAN service template: SSID, VLAN and security.
type ServiceTemplate struct {
	Name               string   `json:"name"`
	SSID               *string  `json:"ssid"`
	VLANID             *int     `json:"vlan_id"`
	Enabled            *bool    `json:"enabled"`
	AuthenticationType *string  `json:"authentication_type,omitempty"`
	Configuration      Settings `json:"configuration"`
}

// APGroup is a group of APs sharing configuration.
type APGroup struct {
	Name          string              `json:"name"`
	APNames       []string            `json:"ap_names,omitempty"`
	SerialIDs     []string            `json:"serial_ids,omitempty"`
	VLANIDs       []int               `json:"vlan_ids,omitempty"`
	Configuration Settings            `json:"configuration"`
	ModelProfiles map[string]Settings `json:"model_profiles,omitempty"` // AP model -> settings
}

// TemplateBinding binds a service template to a radio, optionally on a VLAN.
type TemplateBinding struct {
	Name   string `json:"name"`
	VLANID *int   `json:"vlan_id"`
}

// RateLimit is one direction of client rate limiting.
type RateLimit struct {
	Mode   string  `json:"mode,omitempty"`
	CIRBps *Number `json:"cir_bps,omitempty"`
}

// AntiSticky configures sticky-client steering.
type AntiSticky struct {
	Enabled  *bool    `json:"enabled,omitempty"`
	Interval *Number  `json:"interval,omitempty"`
	RSSI     *float64 `json:"rssi"`
}

// Rates are radio data-rate sets in Mbps.
type Rates struct {
	Disabled  []float64 `json:"disabled,omitempty"`
	Mandatory []float64 `json:"mandatory,omitempty"`
	Supported []float64 `json:"supported,omitempty"`
	Multicast string    `json:"multicast,omitempty"`
}

// Portal configures portal authentication on a template.
type Portal struct {
	Domain                string `json:"domain,omitempty"`
	BASIP                 string `json:"bas_ip,omitempty"`
	EnableMethod          string `json:"enable_method,omitempty"`
	ApplyWebServer        *bool  `json:"apply_web_server,omitempty"`
	ApplyMACTriggerServer string `json:"apply_mac_trigger_server,omitempty"`
}

// LicenseGroup is the AC license-sharing group.
type LicenseGroup struct {
	LocalIP                string   `json:"local_ip,omitempty"`
	MemberIPs              []string `json:"member_ips,omitempty"`
	LicenseSynchronization *bool    `json:"license_synchronization,omitempty"`
}

// AntiBMC is RROP anti broadcast/multicast control.
type AntiBMC struct {
	DefaultAction string `json:"default_action,omitempty"`
	Network       *struct {
		Mode    string `json:"mode"`
		Enabled bool   `json:"enabled"`
	} `json:"network,omitempty"`
}

// Settings is the union of configuration statements that the parser places
// on a scope: global, AP group, AP, radio, band default, model profile,
// virtual AP group or service template. Every field is optional; which
// fields a scope can carry is decided by the vendor parser.
type Settings struct {
	// Identity of an AP scope.
	Name             string  `json:"name,omitempty"`
	Model            string  `json:"model,omitempty"`
	SerialID         string  `json:"serial_id,omitempty"`
	Description      string  `json:"description,omitempty"`
	Type             string  `json:"type,omitempty"`
	Priority         *Number `json:"priority,omitempty"`
	MapConfiguration string  `json:"map_configuration,omitempty"`

	// Controller and AP management.
	Hostname                string        `json:"hostname,omitempty"`
	NASID                   string        `json:"nas_id,omitempty"`
	RegionCodeLock          *bool         `json:"region_code_lock,omitempty"`
	AutoAP                  *bool         `json:"auto_ap,omitempty"`
	AutoPersistent          *bool         `json:"auto_persistent,omitempty"`
	BackupACIP              string        `json:"backup_ac_ip,omitempty"`
	TunnelPreempt           *bool         `json:"tunnel_preempt,omitempty"`
	FirmwareUpgrade         *bool         `json:"firmware_upgrade,omitempty"`
	ImageLoadFilepath       string        `json:"image_load_filepath,omitempty"`
	LEDMode                 string        `json:"led_mode,omitempty"`
	StatisticsInterval      *Number       `json:"statistics_interval,omitempty"`
	TCPMSS                  *Number       `json:"tcp_mss,omitempty"`
	FastForwardingMode      string        `json:"fast_forwarding_mode,omitempty"`
	ClientInspect           *bool         `json:"client_inspect,omitempty"`
	BandNavigation          *bool         `json:"band_navigation,omitempty"`
	RadioLoadBalanceBandNav *bool         `json:"radio_load_balance_band_navigation,omitempty"`
	APLicenseGroup          *LicenseGroup `json:"ap_license_group,omitempty"`
	VIPClientGroup          *struct {
		MACAddresses []string `json:"mac_addresses"`
	} `json:"vip_client_group,omitempty"`
	VirtualAPGroup map[string]Settings `json:"virtual_ap_group,omitempty"`
	RROPAntiBMC    *AntiBMC            `json:"rrop_anti_bmc,omitempty"`

	// Radio scope.
	Enabled             *bool               `json:"enabled,omitempty"`
	Channel             string              `json:"channel,omitempty"`
	ChannelNumber       *int                `json:"channel_number,omitempty"`
	ChannelUnlocked     *bool               `json:"channel_unlocked,omitempty"`
	ChannelBandwidth    string              `json:"channel_bandwidth,omitempty"`
	MaxPower            *float64            `json:"max_power,omitempty"`
	CalibrateChannel    *bool               `json:"calibrate_channel,omitempty"`
	CalibratePower      *bool               `json:"calibrate_power,omitempty"`
	RadarDetectDisabled *bool               `json:"radar_detect_disabled,omitempty"`
	AntiSticky          *AntiSticky         `json:"anti_sticky,omitempty"`
	ClientOptions       map[string]float64  `json:"client_options,omitempty"`
	PacketFilters       map[string]string   `json:"packet_filters,omitempty"`
	Rates               *Rates              `json:"rates,omitempty"`
	ServiceTemplates    []TemplateBinding   `json:"service_templates,omitempty"`
	BandDefaults        map[string]Settings `json:"band_defaults,omitempty"` // 2.4g, 5g, 6g
	Radios              map[string]Settings `json:"radios,omitempty"`        // radio id
	EthernetPorts       map[string]Settings `json:"ethernet_ports,omitempty"`

	// Service template security and client policy.
	AKMModes                         []string             `json:"akm_modes,omitempty"`
	CipherSuites                     []string             `json:"cipher_suites,omitempty"`
	SecurityIEs                      []string             `json:"security_ies,omitempty"`
	HasPresharedKey                  *bool                `json:"has_preshared_key,omitempty"`
	SSIDHidden                       *bool                `json:"ssid_hidden,omitempty"`
	BroadcastProbeReply              *bool                `json:"broadcast_probe_reply,omitempty"`
	ClientSecurityAuthenticationMode string               `json:"client_security_authentication_mode,omitempty"`
	ClientMaxCount                   *Number              `json:"client_max_count,omitempty"`
	ClientKeepAlive                  *bool                `json:"client_keep_alive,omitempty"`
	ClientKeepAliveInterval          *Number              `json:"client_keep_alive_interval,omitempty"`
	ClientCacheAgingTime             *Number              `json:"client_cache_aging_time,omitempty"`
	ClientRateLimitEnabled           *bool                `json:"client_rate_limit_enabled,omitempty"`
	ClientRateLimit                  map[string]RateLimit `json:"client_rate_limit,omitempty"` // inbound, outbound
	BTMEnabled                       *bool                `json:"btm_enabled,omitempty"`
	BTMDisassociationTimer           *Number              `json:"btm_disassociation_timer,omitempty"`
	ForwardingLocation               string               `json:"forwarding_location,omitempty"`
	Portal                           *Portal              `json:"portal,omitempty"`
	IgnoreAuthentication             *bool                `json:"ignore_authentication,omitempty"`
	IgnoreAuthorization              *bool                `json:"ignore_authorization,omitempty"`

	// 802.1X and MAC authentication.
	Dot1xEnabled                    *bool             `json:"dot1x_enabled,omitempty"`
	Dot1xDomain                     string            `json:"dot1x_domain,omitempty"`
	Dot1xAuthenticationMethod       string            `json:"dot1x_authentication_method,omitempty"`
	Dot1xRetry                      *Number           `json:"dot1x_retry,omitempty"`
	Dot1xTimers                     map[string]Number `json:"dot1x_timers,omitempty"`
	MACAuthenticationEnabled        *bool             `json:"mac_authentication_enabled,omitempty"`
	MACAuthenticationDomain         string            `json:"mac_authentication_domain,omitempty"`
	MACAuthenticationHasPassword    *bool             `json:"mac_authentication_has_password,omitempty"`
	MACAuthenticationUserNameFormat string            `json:"mac_authentication_user_name_format,omitempty"`
}
