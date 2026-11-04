package cowork

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// Public source reading cannot become a route to local app/debug servers or
// machine metadata. Resolve at dial time and connect to the checked IP, so a
// redirect or DNS change cannot bypass the boundary. No ambient proxy auth.
func publicTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range addresses {
			if !publicAddress(ip) {
				continue
			}
			return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, errors.New("source URL must resolve to a public network address")
	}
	return transport
}
func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}
