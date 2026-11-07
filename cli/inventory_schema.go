package cli

// Inventory settings supported by the connector v0.2 public schema.
var inventoryFields = map[string]map[string]envField{
	"netdisco": {
		"cache_directory":     {"ND_CACHE_DIRECTORY", "string"},
		"cache_namespace":     {"ND_CACHE_NAMESPACE", "optional_string"},
		"cache_ttl":           {"ND_CACHE_TTL", "number"},
		"cache_enabled":       {"ND_CACHE_ENABLED", "boolean"},
		"cache_mode":          {"ND_CACHE_MODE", "string"},
		"source":              {"ND_SOURCE", "string"},
		"query":               {"ND_QUERY", "string"},
		"query_params":        {"ND_QUERY_PARAMS", "json_array"},
		"url":                 {"ND_URL", "string"},
		"page_size":           {"ND_PAGE_SIZE", "integer"},
		"max_pages":           {"ND_MAX_PAGES", "integer"},
		"max_response_bytes":  {"ND_MAX_RESPONSE_BYTES", "integer"},
		"max_inventory_bytes": {"ND_MAX_INVENTORY_BYTES", "integer"},
		"max_devices":         {"ND_MAX_DEVICES", "integer"},
		"inventory_timeout":   {"ND_INVENTORY_TIMEOUT", "number"},
		"allow_insecure_http": {"ND_ALLOW_INSECURE_HTTP", "boolean"},
		"db_port":             {"ND_DB_PORT", "integer"},
		"db_sslmode":          {"ND_DB_SSLMODE", "string"},
		"db_sslrootcert":      {"ND_DB_SSLROOTCERT", "string"},
		"db_connect_timeout":  {"ND_DB_CONNECT_TIMEOUT", "integer"},
	},
	"inventory": {
		"include_hosts":    {"NC_INCLUDE_HOSTS", "list"},
		"exclude_hosts":    {"NC_EXCLUDE_HOSTS", "list"},
		"include_names":    {"NC_INCLUDE_NAMES", "list"},
		"exclude_names":    {"NC_EXCLUDE_NAMES", "list"},
		"name_match":       {"NC_NAME_MATCH", "string"},
		"include_models":   {"NC_INCLUDE_MODELS", "list"},
		"exclude_models":   {"NC_EXCLUDE_MODELS", "list"},
		"model_match":      {"NC_MODEL_MATCH", "string"},
		"exclude_vendors":  {"NC_EXCLUDE_VENDORS", "list"},
		"include_vendors":  {"NC_INCLUDE_VENDORS", "list"},
		"vendor_overrides": {"NC_VENDOR_OVERRIDES", "json"},
		"host_overrides":   {"NC_HOST_OVERRIDES", "json"},
		"device_rules":     {"NC_DEVICE_RULES", "json"},
	},
}

type envField struct{ Key, Kind string }
