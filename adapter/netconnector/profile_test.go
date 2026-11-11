package netconnector

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/gatework/go-net-connector/connector"
	ncvendors "github.com/gatework/go-net-connector/vendors"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/collect"
	"github.com/gatework/go-wireless/parser"
	"github.com/gatework/go-wireless/vendors"
	"github.com/gatework/go-wireless/vendors/h3c"
)

func TestNativeSelectedProfileDrivesControllerAndAPQueries(t *testing.T) {
	for _, prefix := range []string{"show custom", "display selected"} {
		t.Run(prefix, func(t *testing.T) {
			spec := h3c.Profile()
			spec.Preparation = prefix + " controller preparation"
			spec.APPreparation = prefix + " ap preparation"
			commands := map[wireless.Dimension]string{
				wireless.AccessPoints: prefix + " access points",
				wireless.Clients:      prefix + " clients",
				wireless.LLDP:         prefix + " neighbors",
			}
			for i := range spec.Queries {
				for dimension, command := range commands {
					if slices.Contains(spec.Queries[i].Dimensions, dimension) {
						spec.Queries[i].Command = command
					}
				}
			}
			registry, err := parser.NewRegistry(spec)
			if err != nil {
				t.Fatal(err)
			}
			profile, _ := registry.Fetch("h3c", "")
			inventory := "Total number of APs : 1\r\nAP name : AP-1\r\nAP ID : 1\r\nState : Run\r\nIP address : 192.0.2.7\r\n"
			neighbors := commands[wireless.LLDP] + "\r\nLLDP neighbor-information of port 1[GE1]:\r\nLLDP neighbor index: 1\r\nChassis ID: switch\r\nPort ID: GE2\r\n<AP>"
			steps := []exchange{
				{spec.Preparation + "\n", "<WLC>"},
				{commands[wireless.Clients] + "\n", "Total number of clients : 0\r\n<WLC>"},
				{commands[wireless.AccessPoints] + "\n", inventory + "<WLC>"},
			}
			steps = append(steps, loginSteps()...)
			steps = append(steps,
				exchange{spec.APPreparation + "\n", "<AP>"},
				exchange{commands[wireless.LLDP] + "\n", neighbors},
				exchange{"quit\n", "<WLC>"},
			)
			device, transport := fixture(t, steps, false, nil)
			open := Open(device, profile)
			dimensions := []wireless.Dimension{wireless.Clients, wireless.LLDP}
			options := collect.Request{
				Target: wireless.Record{"host": "fixture"}, Profile: profile,
				ConnectorPlatform: device.ProfileName(), Dimensions: dimensions,
				APCredentials: func(context.Context) (collect.APCredentials, error) {
					return collect.APCredentials{Username: "ap-user", Password: "ap-secret"}, nil
				},
				Open: func(ctx context.Context, run func(collect.Session) error) error {
					// Collection and adapter admission own the selected declarations.
					*profile = parser.Profile{}
					spec.Preparation, spec.APPreparation = "changed controller", "changed ap"
					for i := range spec.Queries {
						spec.Queries[i].Command = "changed query"
						spec.Queries[i].Dimensions[0] = wireless.Configuration
					}
					dimensions[0] = wireless.Configuration
					return open(ctx, run)
				},
			}
			snapshot, err := collect.NewCollector(collect.CollectorOptions{APFactory: APFactory(nil)}).Collect(t.Context(), options)
			if err != nil || !snapshot.Complete() || !snapshot.Dimension(wireless.Clients).Complete() {
				t.Fatalf("selected collection: %v, %v", snapshot.ToMap(), err)
			}
			records := snapshot.Records(wireless.LLDP)
			if len(records) != 1 || records[0]["ap_name"] != "AP-1" {
				t.Fatal("AP query did not reach the selected parser", records)
			}
			for dimension, command := range commands {
				evidence := snapshot.Dimension(dimension).Evidence()
				if len(evidence) != 1 || evidence[0]["command"] != command {
					t.Fatalf("%s source: %v", dimension, evidence)
				}
			}
			evidence := snapshot.Dimension(wireless.LLDP).Evidence()[0]
			if evidence["ap_name"] != "AP-1" || evidence["sha256"] != fmt.Sprintf("%x", sha256.Sum256([]byte(neighbors))) {
				t.Fatal("AP evidence was rebound", evidence)
			}
			if device.Connected() || transport.closes.Load() != 1 {
				t.Fatal("collection did not own one close")
			}
		})
	}
}

func TestNativeProfilesRejectedBeforeConnection(t *testing.T) {
	driver, _ := ncvendors.NewBuiltinRegistry().Lookup("h3c_wireless")
	device, err := connector.New(driver.Profile(), connector.ConnectionConfig{Host: "fixture.invalid"}, connector.WithTransportFactory(func(context.Context, connector.ConnectionConfig) (io.ReadWriteCloser, error) {
		t.Fatal("invalid profile opened a transport")
		return nil, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	huawei, _ := vendors.NewBuiltinRegistry().Fetch("huawei", "")
	for _, profile := range []*parser.Profile{nil, {}, huawei} {
		if _, err := Borrow(device, profile); err == nil {
			t.Fatal("invalid profile borrowed a device")
		}
		if err := Open(device, profile)(t.Context(), func(collect.Session) error {
			t.Fatal("invalid profile admitted a callback")
			return nil
		}); err == nil {
			t.Fatal("invalid profile opened a session")
		}
		if _, err := NewAPSession(device, profile, collect.APCredentials{Password: "valid"}, nil); err == nil {
			t.Fatal("invalid AP profile accepted")
		}
	}
}

func TestNativeAPQueryRequiresSelectedProfileBeforeWriting(t *testing.T) {
	device, _ := fixture(t, nil, false, nil)
	profile := selectedProfile()
	ap, err := NewAPSession(device, profile, collect.APCredentials{Password: "valid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	foreignRegistry, err := parser.NewRegistry(h3c.Profile())
	if err != nil {
		t.Fatal(err)
	}
	foreign, _ := foreignRegistry.Fetch("h3c", "")
	for _, query := range []parser.APQuery{{}, foreign.APQuery()} {
		outcome, err := ap.Read(t.Context(), collect.APRequest{AccessPoint: apRecord(), Query: query})
		if err != nil || !outcome.Skipped || outcome.Error == nil || outcome.Error.Code() != "invalid_ap_query" || !device.Connected() || !ap.usable.Load() {
			t.Fatalf("invalid query changed session: %v, %v", outcome, err)
		}
	}
	controller, _ := Borrow(device, profile)
	if _, err := controller.Execute(t.Context(), profile.APQuery().Command()); err == nil {
		t.Fatal("AP-only query accepted on the controller")
	}
}

func TestNativeAPRequestAndProfileOwnedDuringOperation(t *testing.T) {
	profile := selectedProfile()
	request := collect.APRequest{AccessPoint: apRecord(), Query: profile.APQuery()}
	steps := append(loginSteps(),
		exchange{request.Query.PreparationCommand() + "\n", "<AP>"},
		exchange{request.Query.Command() + "\n", "No LLDP neighbors\r\n<AP>"},
		exchange{"quit\n", "<WLC>"},
	)
	changed := false
	device, _ := fixture(t, steps, false, func(event connector.SessionEvent) {
		if event.Name == "command_start" && !changed {
			changed = true
			request.AccessPoint["management_ip"] = "192.0.2.99"
			request.AccessPoint["name"] = "changed AP"
			request.Query = parser.APQuery{}
			*profile = parser.Profile{}
		}
	})
	ap, err := NewAPSession(device, profile, collect.APCredentials{Username: "ap-user", Password: "ap-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := ap.Read(t.Context(), request)
	if err != nil || outcome.Error != nil || outcome.CleanupError != nil || outcome.Result == nil || !outcome.Result.Success() || !changed || device.RawPrompt() != "<WLC>" {
		t.Fatalf("caller changed admitted AP work: %v, %v", outcome, err)
	}
}

func TestNativeHuaweiCannotAcquireAPDialogue(t *testing.T) {
	driver, _ := ncvendors.NewBuiltinRegistry().Lookup("huawei")
	device, err := connector.New(driver.Profile(), connector.ConnectionConfig{Host: "fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := vendors.NewBuiltinRegistry().Fetch("huawei", "")
	if _, err := Borrow(device, profile); err != nil {
		t.Fatal("supported controller profile rejected", err)
	}
	if _, err := NewAPSession(device, profile, collect.APCredentials{Password: "valid"}, nil); err == nil {
		t.Fatal("unsupported AP dialogue accepted")
	}
}
