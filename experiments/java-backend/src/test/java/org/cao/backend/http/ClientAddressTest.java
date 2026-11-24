package org.cao.backend.http;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.net.InetAddress;
import java.util.List;
import org.junit.jupiter.api.Test;

/** Specification §4 and conformance items 6 and 7 of specs/server-rate-limiting.md. */
class ClientAddressTest {
    private static final InetAddress PROXY = ip("10.0.0.5");
    private static final ProxyPolicy TRUSTED = new ProxyPolicy(List.of("cao.example.com"), true, true,
        List.of(ProxyPolicy.Cidr.parse("10.0.0.0/8"), ProxyPolicy.Cidr.parse("fd00::/8")));
    private static final ProxyPolicy DIRECT = new ProxyPolicy(List.of("cao.example.com"), true, false,
        List.of());

    private static InetAddress ip(String text) {
        return IpLiteral.parseWithOptionalPort(text).orElseThrow();
    }

    private static String resolve(ProxyPolicy policy, InetAddress peer, List<String> xff,
        List<String> forwarded) {
        return ClientAddress.resolve(policy, peer, xff, forwarded).address();
    }

    @Test
    void directListenerIgnoresEveryForwardingHeader() {
        ClientAddress.Resolved r = ClientAddress.resolve(DIRECT, ip("192.0.2.7"),
            List.of("203.0.113.9"), List.of("for=198.51.100.1"));
        assertEquals("192.0.2.7", r.address());
        assertEquals(ClientAddress.Source.UNTRUSTED_BOUNDARY, r.source());
    }

    @Test
    void untrustedPeerCannotInjectForwardedAddressesEvenWhenForwardingIsEnabled() {
        assertEquals("192.0.2.7", resolve(TRUSTED, ip("192.0.2.7"), List.of("203.0.113.9"), List.of()));
    }

    @Test
    void onlyTheFinalValueOfTheFinalFieldIsUsed() {
        assertEquals("203.0.113.9",
            resolve(TRUSTED, PROXY, List.of("198.51.100.1, 198.51.100.2", "192.0.2.1,203.0.113.9"), List.of()));
        assertEquals("203.0.113.9",
            resolve(TRUSTED, PROXY, List.of("198.51.100.1", "203.0.113.9, ,"), List.of()));
    }

    @Test
    void malformedFinalValueFallsBackToThePeerNotAnEarlierValue() {
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of("203.0.113.9, unknown"), List.of()));
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of("203.0.113.9, _hidden"), List.of()));
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of("203.0.113.9, 999.1.1.1"), List.of()));
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of("203.0.113.9, 01.2.3.4"), List.of()));
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of("203.0.113.9, example.com"), List.of()));
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of("203.0.113.9, 1.2.3.4:99999"), List.of()));
    }

    @Test
    void ipv4Ipv6AndPortsAreAccepted() {
        assertEquals("203.0.113.9", resolve(TRUSTED, PROXY, List.of("203.0.113.9:4567"), List.of()));
        assertEquals("2001:db8::8", resolve(TRUSTED, PROXY, List.of("2001:DB8:0:0:0:0:0:8"), List.of()));
        assertEquals("2001:db8::8", resolve(TRUSTED, PROXY, List.of("[2001:db8::8]:4567"), List.of()));
        assertEquals("2001:db8::8", resolve(TRUSTED, PROXY, List.of("[2001:db8::8]"), List.of()));
        assertEquals("203.0.113.9", resolve(TRUSTED, PROXY, List.of("::ffff:203.0.113.9"), List.of()));
        assertEquals("::1", resolve(TRUSTED, PROXY, List.of("::1"), List.of()));
        assertEquals("2001:db8::1:0:0:1", resolve(TRUSTED, PROXY,
            List.of("2001:db8:0:0:1:0:0:1"), List.of()));
    }

    @Test
    void rfc7239ForwardedIsUsedWhenXForwardedForIsAbsent() {
        assertEquals("203.0.113.9",
            resolve(TRUSTED, PROXY, List.of(), List.of("for=203.0.113.9;proto=https")));
        assertEquals("2001:db8::8", resolve(TRUSTED, PROXY, List.of(),
            List.of("for=203.0.113.9;proto=http", "FOR=\"[2001:db8::8]:4567\";proto=https")));
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of(), List.of("for=unknown;proto=https")));
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of(), List.of("for=_obf")));
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of(), List.of("proto=https")));
    }

    @Test
    void xForwardedForTakesPrecedenceAndAMalformedOneDoesNotFallThroughToForwarded() {
        assertEquals("198.51.100.8", resolve(TRUSTED, PROXY, List.of("198.51.100.8"),
            List.of("for=203.0.113.9")));
        assertEquals("10.0.0.5", resolve(TRUSTED, PROXY, List.of("unknown"), List.of("for=203.0.113.9")));
    }

    @Test
    void ipv6PeerInsideATrustedPrefixIsTrusted() {
        assertEquals("203.0.113.9", resolve(TRUSTED, ip("fd00::7"), List.of("203.0.113.9"), List.of()));
        assertEquals("fe80::7", resolve(TRUSTED, ip("fe80::7"), List.of("203.0.113.9"), List.of()));
    }

    @Test
    void forwardedTrustWithoutExplicitProxiesIsRejected() {
        assertThrows(IllegalArgumentException.class,
            () -> new ProxyPolicy(List.of("cao.example.com"), true, true, List.of()));
        assertThrows(IllegalArgumentException.class, () -> new ProxyPolicy(List.of(), true, false, List.of()));
        assertThrows(IllegalArgumentException.class, () -> ProxyPolicy.Cidr.parse("10.0.0.0/33"));
        assertThrows(IllegalArgumentException.class, () -> ProxyPolicy.Cidr.parse("not-an-ip/8"));
        assertThrows(IllegalArgumentException.class, () -> ProxyPolicy.Cidr.parse("10.0.0.0/x"));
    }

    @Test
    void hostAndProtocolBoundary() {
        assertEquals(ProxyPolicy.Rejection.NONE, TRUSTED.classify("ignored", false,
            List.of("cao.example.com"), List.of("https"), PROXY));
        assertEquals(ProxyPolicy.Rejection.NONE, TRUSTED.classify(null, false,
            List.of("x.example, cao.example.com:443"), List.of("http, https"), PROXY));
        assertEquals(ProxyPolicy.Rejection.INSECURE_REQUEST, TRUSTED.classify(null, true,
            List.of("cao.example.com"), List.of("http"), PROXY));
        assertEquals(ProxyPolicy.Rejection.HOST_NOT_ALLOWED, TRUSTED.classify(null, false,
            List.of("evil.example"), List.of("https"), PROXY));
        assertEquals(ProxyPolicy.Rejection.UNTRUSTED_PEER, TRUSTED.classify("cao.example.com", true,
            List.of(), List.of("https"), ip("192.0.2.7")));
        assertEquals(ProxyPolicy.Rejection.EMPTY_HOST, DIRECT.classify(null, true, List.of(), List.of(), PROXY));
        assertEquals(ProxyPolicy.Rejection.NONE, DIRECT.classify("CAO.example.com:8443", true,
            List.of("evil.example"), List.of("http"), PROXY));
        assertEquals(ProxyPolicy.Rejection.INSECURE_REQUEST, DIRECT.classify("cao.example.com", false,
            List.of(), List.of("https"), PROXY));
    }
}
