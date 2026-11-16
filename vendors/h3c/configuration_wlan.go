package h3c

import (
	"errors"

	wireless "github.com/gatework/go-wireless"
	recordvalue "github.com/gatework/go-wireless/internal/record"
	"github.com/gatework/go-wireless/parser"
)

func apMACSetting(l *settingLine) error {
	value, err := parser.ParseMAC(&l.capture[1])
	if err != nil {
		return err
	}
	l.assign("mac_address", recordvalue.Scalar(value))
	return nil
}

func loadBalanceSetting(l *settingLine) error {
	value, err := boundedSetting(l.capture[1], 1, 120)
	if err != nil {
		return err
	}
	l.parser.assignMany(ensureMap(l.record, "load_balance"), "mode", wireless.Record{"mode": "session", "session_threshold": value}, l.block)
	return nil
}

func roamNavigationSetting(l *settingLine) error {
	rssi, err := boundedSetting(l.capture[1], 1, 50)
	if err != nil {
		return err
	}
	power, err := boundedSetting(l.capture[2], 1, 30)
	if err != nil {
		return err
	}
	l.assign("roam_navigation", wireless.Record{"enabled": true, "rssi": rssi, "probe_response_power_dbm": power})
	return nil
}

func boundedSetting(raw string, minimum, maximum int64) (int64, error) {
	value, err := parser.ParseInteger(&raw, &maximum)
	if err != nil {
		return 0, err
	}
	if value == nil || *value < minimum {
		return 0, errors.New("wireless: invalid setting range")
	}
	return *value, nil
}
