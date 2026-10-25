package cliegress

import (
	"context"
	"net/netip"
	"testing"
)

func TestPolicyValidatesEveryResolvedAddressBeforeNumericDial(t *testing.T) {
	policy, err := ParsePolicy([]byte(`{"hosts":["API.GITHUB.COM."],"privateEndpoints":[{"host":"github.corp.example","addresses":["10.2.3.4","fd12::1"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host      string
		addresses []string
		want      string
	}{
		{"api.github.com:443", []string{"140.82.114.5"}, "140.82.114.5:443"},
		{"API.GITHUB.COM.:443", []string{"::ffff:140.82.114.5"}, "140.82.114.5:443"},
		{"api.github.com:443", []string{"2606:50c0:8000::154"}, "[2606:50c0:8000::154]:443"},
		{"github.corp.example:443", []string{"::ffff:10.2.3.4", "fd12::1"}, "10.2.3.4:443"},
		{"github.corp.example:443", []string{"10.2.3.5"}, ""},
		{"github.corp.example:443", []string{"140.82.114.5"}, ""},
		{"github.corp.example:443", []string{"169.254.169.254"}, ""},
		{"api.github.com:443", []string{"140.82.114.5", "10.2.3.4"}, ""},
		{"api.github.com:443", nil, ""},
		{"api.github.com:80", []string{"140.82.114.5"}, ""},
		{"api.github.com:0443", []string{"140.82.114.5"}, ""},
		{"api.github.com.evil.example:443", []string{"140.82.114.5"}, ""},
		{"api.github.com@evil.example:443", []string{"140.82.114.5"}, ""},
		{"140.82.114.5:443", []string{"140.82.114.5"}, ""},
		{"[::ffff:140.82.114.5]:443", []string{"140.82.114.5"}, ""},
	} {
		t.Run(tc.host+"/"+tc.want, func(t *testing.T) {
			lookup := func(_ context.Context, _, host string) ([]netip.Addr, error) {
				if host != "api.github.com" && host != "github.corp.example" {
					t.Fatal("unapproved host reached DNS")
				}
				var ips []netip.Addr
				for _, value := range tc.addresses {
					ips = append(ips, netip.MustParseAddr(value))
				}
				return ips, nil
			}
			addresses, err := policy.destination(t.Context(), tc.host, lookup)
			got := ""
			if len(addresses) != 0 {
				got = addresses[0]
			}
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("destination = %q, %v", addresses, err)
			}
		})
	}
}

func TestPolicyRejectsSpecialAddressesAndPrivateMetadataExceptions(t *testing.T) {
	policy, err := ParsePolicy([]byte(`{"hosts":["api.github.com"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"0.0.0.0", "10.1.2.3", "100.100.100.200", "127.0.0.1", "169.254.169.254",
		"168.63.129.16", "172.17.0.1", "192.168.1.1", "192.0.2.1", "198.18.0.1",
		"224.0.0.1", "240.0.0.1", "::", "::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254",
		"::127.0.0.1", "64:ff9b::a9fe:a9fe", "64:ff9b:1::1", "fe80::1", "fe80::1%eth0",
		"fd00:ec2::254", "fd20:ce::254", "fd12::1", "ff02::1", "2001::1", "2002:7f00:1::", "2001:db8::1", "3fff::1",
	} {
		t.Run(raw, func(t *testing.T) {
			lookup := func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr(raw)}, nil
			}
			if _, err := policy.destination(t.Context(), "api.github.com:443", lookup); err == nil {
				t.Fatal("special-use address accepted")
			}
		})
	}
	for _, raw := range []string{"127.0.0.1", "169.254.169.254", "::ffff:169.254.169.254", "fd00:ec2::254", "fd20:ce::254", "fe80::1", "168.63.129.16", "100.100.100.200", "10.0.0.0/8"} {
		data := `{"privateEndpoints":[{"host":"ghes.example.com","addresses":["` + raw + `"]}]}`
		if _, err := ParsePolicy([]byte(data)); err == nil {
			t.Fatalf("private exception accepted %s", raw)
		}
	}
}

func TestPolicyRejectsAmbiguousConfiguration(t *testing.T) {
	for _, data := range []string{
		`{}`, `null`, `{"hosts":[]}`, `{"hosts":["*"]}`, `{"hosts":["*.github.com"]}`,
		`{"hosts":["github.com:443"]}`, `{"hosts":["github.com"],"ports":[443]}`,
		`{"hosts":["github.com"]} {}`, `{"hosts":["github.com","GITHUB.COM."]}`,
		`{"hosts":["127.0.0.1"]}`, `{"hosts":["localhost"]}`, `{"hosts":["github.com.."]}`,
		`{"privateEndpoints":[{"host":"ghes.example.com","addresses":[]}]}`,
		`{"privateEndpoints":[{"host":"ghes.example.com","addresses":["10.0.0.1","::ffff:10.0.0.1"]}]}`,
	} {
		if _, err := ParsePolicy([]byte(data)); err == nil {
			t.Fatalf("invalid policy accepted: %s", data)
		}
	}
}

func TestFirstDeniedAddress(t *testing.T) {
	addr := netip.MustParseAddr
	for _, tc := range []struct {
		name      string
		addresses []netip.Addr
		private   []string
		want      int
	}{
		{"all public", []netip.Addr{addr("140.82.114.5"), addr("2606:50c0:8000::154")}, nil, -1},
		{"mapped public", []netip.Addr{addr("::ffff:140.82.114.5")}, nil, -1},
		{"private in public host", []netip.Addr{addr("140.82.114.5"), addr("10.2.3.4")}, nil, 1},
		{"metadata", []netip.Addr{addr("169.254.169.254")}, nil, 0},
		{"exact private", []netip.Addr{addr("::ffff:10.2.3.4"), addr("fd12::1")}, []string{"10.2.3.4", "fd12::1"}, -1},
		{"other private", []netip.Addr{addr("10.2.3.4"), addr("10.2.3.5")}, []string{"10.2.3.4"}, 1},
		{"public on private endpoint", []netip.Addr{addr("140.82.114.5")}, []string{"10.2.3.4"}, 0},
		{"empty", nil, nil, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstDeniedAddress(tc.addresses, tc.private); got != tc.want {
				t.Fatalf("firstDeniedAddress = %d, want %d", got, tc.want)
			}
		})
	}
}
