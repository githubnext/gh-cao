// Package cliegress implements the destination-only boundary for hosted CLI
// HTTPS tunnels. It does not inspect TLS SNI or application content.
package cliegress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
)

const MaxPolicyBytes = 16384

type Policy struct {
	Hosts            []string          `json:"hosts"`
	PrivateEndpoints []PrivateEndpoint `json:"privateEndpoints,omitempty"`
}

type PrivateEndpoint struct {
	Host      string   `json:"host"`
	Addresses []string `json:"addresses"`
}

// ParsePolicy accepts exact DNS names and HTTPS/443 only. There are no implicit
// hosts, suffix matches, CIDRs, system-proxy settings, or alternate ports.
func ParsePolicy(data []byte) (*Policy, error) {
	if len(data) > MaxPolicyBytes {
		return nil, errors.New("hosted CLI egress policy exceeds 16 KiB")
	}
	var p Policy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("hosted CLI egress policy must be one valid JSON object with known fields")
	}
	if len(p.Hosts)+len(p.PrivateEndpoints) == 0 || len(p.Hosts)+len(p.PrivateEndpoints) > 32 {
		return nil, errors.New("hosted CLI egress policy requires 1 to 32 exact hosts")
	}
	seen := map[string]bool{}
	checkHost := func(host string) (string, error) {
		host, ok := canonicalHost(host)
		if !ok || seen[host] {
			return "", errors.New("hosted CLI egress policy contains an invalid or duplicate host")
		}
		seen[host] = true
		return host, nil
	}
	for i, host := range p.Hosts {
		var err error
		if p.Hosts[i], err = checkHost(host); err != nil {
			return nil, err
		}
	}
	for i := range p.PrivateEndpoints {
		endpoint := &p.PrivateEndpoints[i]
		var err error
		if endpoint.Host, err = checkHost(endpoint.Host); err != nil {
			return nil, err
		}
		if len(endpoint.Addresses) == 0 || len(endpoint.Addresses) > 16 {
			return nil, errors.New("hosted CLI private GHES endpoint requires 1 to 16 exact private addresses")
		}
		addresses := map[netip.Addr]bool{}
		for j, raw := range endpoint.Addresses {
			ip, err := netip.ParseAddr(raw)
			ip = ip.Unmap()
			if err != nil || !privateAddress(ip) || addresses[ip] {
				return nil, errors.New("hosted CLI GHES addresses must be unique RFC1918 or ULA addresses, never metadata, loopback, or link-local")
			}
			addresses[ip] = true
			endpoint.Addresses[j] = ip.String()
		}
	}
	return &p, nil
}

func canonicalHost(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if len(host) == 0 || len(host) > 253 || !strings.Contains(host, ".") {
		return "", false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", false
			}
		}
	}
	return host, true
}

// Block special-use ranges as well as IsPrivate. In particular, mapped IPv4,
// transition/NAT64 addresses, shared address space and cloud platform addresses
// must not turn a public host into a route to the executor's environment.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/3"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("::/96"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func privateAddress(ip netip.Addr) bool {
	return ip.IsValid() && ip.Zone() == "" && ip.IsPrivate() &&
		!netip.MustParsePrefix("fd00:ec2::/32").Contains(ip) &&
		ip != netip.MustParseAddr("fd20:ce::254")
}

func publicAddress(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

type lookupFunc func(context.Context, string, string) ([]netip.Addr, error)

// firstDeniedAddress returns the index of the first address that is not an
// acceptable dial target, or -1 when all are. Endpoints with exact private
// addresses accept only those; every other host accepts only public addresses.
func firstDeniedAddress(addresses []netip.Addr, private []string) int {
	for i, ip := range addresses {
		ip = ip.Unmap()
		valid := publicAddress(ip)
		if private != nil {
			valid = false
			for _, exact := range private {
				valid = valid || (privateAddress(ip) && ip.String() == exact)
			}
		}
		if !valid {
			return i
		}
	}
	return -1
}

func (p *Policy) destination(ctx context.Context, authority string, lookup lookupFunc) ([]string, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil || port != "443" {
		return nil, errors.New("destination denied")
	}
	host, ok := canonicalHost(host)
	if !ok {
		return nil, errors.New("destination denied")
	}
	allowed := false
	var private []string
	for _, candidate := range p.Hosts {
		allowed = allowed || candidate == host
	}
	for _, endpoint := range p.PrivateEndpoints {
		if endpoint.Host == host {
			allowed, private = true, endpoint.Addresses
		}
	}
	if !allowed {
		return nil, errors.New("destination denied")
	}
	addresses, err := lookup(ctx, "ip", host)
	if err != nil || len(addresses) == 0 || len(addresses) > 64 {
		relayLog.Printf("destination resolution rejected: addresses=%d lookup_failed=%t", len(addresses), err != nil)
		return nil, errors.New("destination resolution unavailable")
	}
	if index := firstDeniedAddress(addresses, private); index >= 0 {
		relayLog.Printf("destination address rejected: addresses=%d index=%d private_endpoint=%t", len(addresses), index, private != nil)
		return nil, errors.New("destination address denied")
	}
	// All candidates are vetted before any dial; preserve numeric fallback
	// when, for example, the executor has no route for the first IPv6 answer.
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, net.JoinHostPort(address.Unmap().String(), port))
	}
	return result, nil
}
