// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package topology

import (
	"reflect"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
)

func fixtureSpec() Spec {
	return Spec{
		Execution:   ExecutionSpec{ServiceSpec: ServiceSpec{Role: "execution", Name: "el"}, RPCPortID: "rpc", WSPortID: "ws"},
		Required:    []ServiceSpec{{Role: "consensus", Name: "cl"}, {Role: "validator", Name: "vc"}},
		GraphQLPath: "/graphql",
	}
}

func fixtureServices() []kurtosis.Service {
	return []kurtosis.Service{
		{Name: "el", UUID: strings.Repeat("a", 32), Status: kurtosis.ServiceStatusRunning, Image: "el:image", PublicIP: "127.0.0.1", PublicPorts: map[string]kurtosis.Port{"rpc": {Number: 18545}, "ws": {Number: 18546}}},
		{Name: "cl", UUID: strings.Repeat("b", 32), Status: kurtosis.ServiceStatusRunning, Image: "cl:image"},
		{Name: "vc", UUID: strings.Repeat("c", 32), Status: kurtosis.ServiceStatusRunning, Image: "vc:image"},
	}
}

func TestDiscoverBindsFullServiceIdentity(t *testing.T) {
	services := fixtureServices()
	snapshot, err := Discover(fixtureSpec(), services)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Execution.UUID != strings.Repeat("a", 32) || snapshot.RPC.URL != "http://127.0.0.1:18545" || snapshot.WebSocket.URL != "ws://127.0.0.1:18546" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	for name, mutate := range map[string]func([]kurtosis.Service){
		"same name different UUID": func(values []kurtosis.Service) { values[0].UUID = strings.Repeat("d", 32) },
		"image drift":              func(values []kurtosis.Service) { values[0].Image = "el:other" },
		"stopped consensus":        func(values []kurtosis.Service) { values[1].Status = kurtosis.ServiceStatusStopped },
		"unknown validator":        func(values []kurtosis.Service) { values[2].Status = kurtosis.ServiceStatusUnknown },
		"port drift": func(values []kurtosis.Service) {
			values[0].PublicPorts["rpc"] = kurtosis.Port{Number: 28545}
		},
	} {
		t.Run(name, func(t *testing.T) {
			copy := fixtureServices()
			mutate(copy)
			current, err := Discover(fixtureSpec(), copy)
			if err == nil && reflect.DeepEqual(current, snapshot) {
				t.Fatal("topology drift unexpectedly matched the persisted snapshot")
			}
		})
	}

	unknown := fixtureServices()
	unknown[2].Status = kurtosis.ServiceStatusUnknown
	if _, err := Discover(fixtureSpec(), unknown); err == nil || !strings.Contains(err.Error(), "RUNNING") {
		t.Fatalf("unknown service status error = %v", err)
	}
}

func TestDiscoverRejectsUndeclaredPersistentParticipantServices(t *testing.T) {
	services := fixtureServices()
	services = append(services, kurtosis.Service{Name: "genesis-generator", UUID: strings.Repeat("d", 32), Status: kurtosis.ServiceStatusStopped, Image: "helper:image"})
	_, err := Discover(fixtureSpec(), services)
	if err != nil {
		t.Fatalf("declared topology with helper service: %v", err)
	}
	extra := kurtosis.Service{Name: "el-2-gqrl-qrysm", UUID: strings.Repeat("e", 32), Status: kurtosis.ServiceStatusRunning, Image: "el:image"}
	services = append(services, extra)
	if _, err := Discover(fixtureSpec(), services); err == nil || !strings.Contains(err.Error(), "undeclared persistent participant") {
		t.Fatalf("Discover extra participant error = %v", err)
	}
}
