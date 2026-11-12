// SPDX-FileCopyrightText: 2026 Balázs Toldi
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v4"
)

// dispatchedGroupJSON runs a device group from simapp.yaml through dispatchGroup
// and returns the body simapp would post to webconsole.
func dispatchedGroupJSON(t *testing.T, groupYaml string) string {
	t.Helper()
	var group DevGroup
	if err := yaml.Unmarshal([]byte(groupYaml), &group); err != nil {
		t.Fatalf("unmarshal group yaml: %v", err)
	}
	SimappConfig = Config{Configuration: &Configuration{}}
	configMessages := make(chan configMessage, 1)
	dispatchGroup(configMessages, &group, add_op, nil)
	select {
	case message := <-configMessages:
		return message.msgPtr.String()
	default:
		t.Fatal("dispatchGroup did not enqueue a message")
		return ""
	}
}

const (
	testPoolV6Dual = "2001:db8:0:500::/57"
	testPoolV6Only = "2001:db8:0:580::/57"
	testDnsV6      = "2001:4860:4860::8888"
	testPcscfV6    = "2001:db8::10"
	testPoolV4     = "172.250.0.0/16"
	testDnsV4      = "8.8.8.8"
)

const ipDomainQosYaml = `
    ue-dnn-qos:
      dnn-mbr-uplink: 2
      dnn-mbr-downlink: 5
      bitrate-unit: Mbps
      traffic-class:
        name: gold
        qci: 9
        arp: 6
        pdb: 300
        pelr: 6
`

// An IPv4-only group posts the same body as before the IPv6 keys were added.
func TestDispatchGroupIpv4OnlyBodyUnchanged(t *testing.T) {
	got := dispatchedGroupJSON(t, `
name: dg-v4
site-info: enterprise
imsis: ["208930100007500"]
ip-domain-name: pool1
ip-domains:
  - dnn: internet
    ue-ip-pool: 172.250.0.0/16
    dns-primary: 8.8.8.8
    dns-secondary: 8.8.4.4
    pcscf-primary: 10.10.10.10
    mtu: 1460`+ipDomainQosYaml)
	want := `{"Name":"dg-v4","site-info":"enterprise","imsis":["208930100007500"],"ip-domain-name":"pool1","ip-domains":[` +
		`{"ue-dnn-qos":{"traffic-class":{"name":"gold","qci":9,"arp":6,"pdb":300,"pelr":6},` +
		`"bitrate-unit":"Mbps","dnn-mbr-uplink":2,"dnn-mbr-downlink":5},"dnn":"internet",` +
		`"dns-primary":"8.8.8.8","pcscf-primary":"10.10.10.10","dns-secondary":"8.8.4.4",` +
		`"ue-ip-pool":"172.250.0.0/16","mtu":1460}]}`
	if got != want {
		t.Errorf("IPv4-only group body changed:\n got %s\nwant %s", got, want)
	}
}

func TestDispatchGroupIpv6IpDomainRoundTrip(t *testing.T) {
	tests := []struct {
		wantKeys map[string]any
		name     string
		yaml     string
		want     IpDomain
	}{
		{
			name: "dual stack",
			yaml: `
name: dg-dual
ip-domain-name: pool1
ip-domains:
  - dnn: internet
    ue-ip-pool: 172.250.0.0/16
    dns-primary: 8.8.8.8
    ue-ipv6-pool: 2001:db8:0:500::/57
    ue-ipv6-prefix-len: 64
    dns-primary-v6: 2001:4860:4860::8888
    pcscf-primary-v6: 2001:db8::10
    mtu: 1460` + ipDomainQosYaml,
			want: IpDomain{
				Dnn: "internet", UePool: testPoolV4, DnsPrimary: testDnsV4,
				UeIpv6Pool: testPoolV6Dual, UeIpv6PrefixLen: 64,
				DnsPrimaryV6: testDnsV6, PcscfPrimaryV6: testPcscfV6, Mtu: 1460,
			},
			wantKeys: map[string]any{
				"ue-ip-pool": testPoolV4, "dns-primary": testDnsV4,
				"ue-ipv6-pool": testPoolV6Dual, "ue-ipv6-prefix-len": float64(64),
				"dns-primary-v6": testDnsV6, "pcscf-primary-v6": testPcscfV6,
			},
		},
		{
			name: "IPv6 only, default prefix length",
			yaml: `
name: dg-v6
ip-domain-name: pool-v6
ip-domains:
  - dnn: internet6
    ue-ipv6-pool: 2001:db8:0:580::/57
    dns-primary-v6: 2001:4860:4860::8888
    mtu: 1460` + ipDomainQosYaml,
			want: IpDomain{
				Dnn: "internet6", UeIpv6Pool: testPoolV6Only,
				DnsPrimaryV6: testDnsV6, Mtu: 1460,
			},
			wantKeys: map[string]any{
				"ue-ipv6-pool": testPoolV6Only, "dns-primary-v6": testDnsV6,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := dispatchedGroupJSON(t, test.yaml)

			// The addressing keys webconsole reads are in the body; unset ones are left out.
			var raw struct {
				IpDomains []map[string]any `json:"ip-domains"`
			}
			if err := json.Unmarshal([]byte(body), &raw); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			if len(raw.IpDomains) != 1 {
				t.Fatalf("body has %d ip-domains, want 1: %s", len(raw.IpDomains), body)
			}
			ipDomain := raw.IpDomains[0]
			for _, key := range []string{"dnn", "mtu", "ue-dnn-qos"} {
				delete(ipDomain, key)
			}
			if !reflect.DeepEqual(ipDomain, test.wantKeys) {
				t.Errorf("ip-domains[0] addressing keys = %v, want %v", ipDomain, test.wantKeys)
			}

			// And the body decodes back to the configured IP domain.
			var dispatched DevGroup
			if err := json.Unmarshal([]byte(body), &dispatched); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			got := dispatched.IpDomains[0]
			got.UeDnnQos = nil
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("round trip ip domain = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestCompareGroupDetectsIpv6IpDomainChanges(t *testing.T) {
	tests := []struct {
		mutate func(*IpDomain)
		name   string
	}{
		{name: "ue-ipv6-pool added", mutate: func(d *IpDomain) { d.UeIpv6Pool = "2001:db8:0:580::/57" }},
		{name: "IPv6 prefix length", mutate: func(d *IpDomain) { d.UeIpv6PrefixLen = 60 }},
		{name: "IPv6 DNS", mutate: func(d *IpDomain) { d.DnsPrimaryV6 = "2001:4860:4860::8844" }},
		{name: "IPv6 P-CSCF", mutate: func(d *IpDomain) { d.PcscfPrimaryV6 = "2001:db8::11" }},
	}
	base := IpDomain{
		Dnn: "internet", UePool: testPoolV4, Mtu: 1460,
		UeIpv6Pool: testPoolV6Dual, UeIpv6PrefixLen: 64,
		DnsPrimaryV6: testDnsV6, PcscfPrimaryV6: testPcscfV6,
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			old := base
			if test.name == "ue-ipv6-pool added" {
				old.UeIpv6Pool = ""
			}
			changed := old
			test.mutate(&changed)
			groupOld := &DevGroup{IpDomains: []IpDomain{old}}
			groupNew := &DevGroup{IpDomains: []IpDomain{changed}}
			if !compareGroup(groupNew, groupOld) {
				t.Fatalf("compareGroup returned false for a %s change", test.name)
			}
		})
	}

	same := &DevGroup{IpDomains: []IpDomain{base}}
	if compareGroup(same, &DevGroup{IpDomains: []IpDomain{base}}) {
		t.Error("compareGroup returned true for an unchanged IPv6 IP domain")
	}
}
