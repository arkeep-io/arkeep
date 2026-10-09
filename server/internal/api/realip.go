package api

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// DefaultTrustedProxies are the networks a reverse proxy usually sits in:
// loopback and the private ranges Docker, Kubernetes and LANs use. A request
// arriving from one of them may name the real client in X-Forwarded-For.
var DefaultTrustedProxies = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
}

// ParseTrustedProxies parses ARKEEP_TRUSTED_PROXIES: a comma-separated list of
// IP addresses and CIDR prefixes. An empty value means DefaultTrustedProxies;
// "none" trusts no proxy, so X-Forwarded-For is always ignored.
func ParseTrustedProxies(spec string) ([]netip.Prefix, error) {
	spec = strings.TrimSpace(spec)
	switch spec {
	case "":
		return DefaultTrustedProxies, nil
	case "none":
		return nil, nil
	}
	var out []netip.Prefix
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("trusted proxies: invalid prefix %q: %w", item, err)
			}
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("trusted proxies: invalid address %q: %w", item, err)
		}
		out = append(out, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
	}
	return out, nil
}

// RealIP rewrites r.RemoteAddr to the client's address when the request comes
// from a trusted proxy (SEC-19). Rate limits and the audit log key on that
// address: behind a proxy every client used to share the proxy's, so one
// client's failed logins locked everyone out.
//
// X-Forwarded-For is read right to left, skipping trusted hops: the rightmost
// address a trusted proxy did not add is the one it saw connect, while
// entries to its left were written by the client and can be forged.
// X-Real-IP is used when X-Forwarded-For is absent.
func RealIP(trusted []netip.Prefix, logger *zap.Logger) func(http.Handler) http.Handler {
	var warnOnce sync.Once
	isTrusted := func(a netip.Addr) bool {
		a = a.Unmap()
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, err := netip.ParseAddr(clientIP(r))
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if !isTrusted(peer) {
				if r.Header.Get("X-Forwarded-For") != "" {
					warnOnce.Do(func() {
						logger.Warn("ignoring X-Forwarded-For from a peer that is not a trusted proxy; if the server is behind a reverse proxy, add its address to ARKEEP_TRUSTED_PROXIES",
							zap.String("peer", peer.String()),
						)
					})
				}
				next.ServeHTTP(w, r)
				return
			}

			if client, ok := forwardedClient(r, isTrusted); ok {
				r.RemoteAddr = net.JoinHostPort(client.String(), "0")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// forwardedClient returns the client address a trusted proxy reported.
func forwardedClient(r *http.Request, isTrusted func(netip.Addr) bool) (netip.Addr, bool) {
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	if len(hops) == 0 {
		if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			return a.Unmap(), true
		}
		return netip.Addr{}, false
	}

	var leftmost netip.Addr
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			// A malformed hop ends the trusted chain: nothing to its left
			// can be attributed to a proxy.
			break
		}
		a = a.Unmap()
		if !isTrusted(a) {
			return a, true
		}
		leftmost = a
	}
	// Every parsed hop is a trusted network: the client itself is on one.
	return leftmost, leftmost.IsValid()
}
