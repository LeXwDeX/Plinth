package tripo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
)

type nativeIPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}
type nativeDialFunc func(context.Context, string, string) (net.Conn, error)

// The anonymous source transport does not reuse the authenticated API client,
// environment proxies, cookies or redirects. net/http performs TLS verification
// against the original URL hostname; DialContext only receives pinned numeric IPs.
func newNativeSourceClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = httpjson.DefaultRequestTimeout
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConnsPerHost: 2, DisableCompression: true}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = nativePinnedDial(net.DefaultResolver, dialer.DialContext)
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func nativePinnedDial(resolver nativeIPResolver, dial nativeDialFunc) nativeDialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" {
			return nil, errors.New("invalid native source address")
		}
		addresses, err := resolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("native source resolution unavailable")
		}
		pinned := make([]string, 0, len(addresses))
		for _, a := range addresses {
			ip, ok := netip.AddrFromSlice(a.IP)
			if !ok || a.Zone != "" || !nativePublicIP(ip.Unmap()) {
				return nil, errors.New("native source address is not public")
			}
			pinned = append(pinned, net.JoinHostPort(ip.Unmap().String(), port))
		}
		var last error
		for _, address := range pinned {
			conn, err := dial(ctx, network, address)
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}
}

var nativeGlobalIPv6Prefix = netip.MustParsePrefix("2000::/3")

var nativeNonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func nativePublicIP(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// IPv6 translation/tunnel/special-use addresses must not hide a private IPv4.
	if ip.Is6() && !nativeGlobalIPv6Prefix.Contains(ip) {
		return false
	}
	for _, prefix := range nativeNonPublicPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func validNativeSourceClaim(claim generation.ArtifactClaim) bool {
	parsed, err := url.Parse(claim.Locator)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == "" && (parsed.Port() == "" || parsed.Port() == "443") && len(claim.Inline) == 0 && claim.MediaType == "model/gltf-binary"
}

func (a *Adapter) bindNativeConversionSource(ctx context.Context, o generation.Offering, claim generation.ArtifactClaim, c *nativeConversionOptions) (nativeConversionSource, error) {
	var empty nativeConversionSource
	if !validNativeSourceClaim(claim) || a.nativeSourceClient == nil {
		return empty, nativeProcessingProtocolError()
	}
	reader, meta, err := httpjson.OpenClaim(ctx, a.nativeSourceClient, claim, o.AssetHosts, false)
	if err != nil {
		return empty, err
	}
	defer reader.Close()
	if meta.Size > c.MaxSourceBytes {
		return empty, failure(generation.FailureIntegrity, false, "Tripo 转换来源超过协议大小上限")
	}
	header := make([]byte, 12)
	if _, err := io.ReadFull(reader, header); err != nil || string(header[:4]) != "glTF" || binary.LittleEndian.Uint32(header[4:]) != 2 {
		return empty, failure(generation.FailureIntegrity, false, "Tripo 转换来源不是有效 GLB")
	}
	declared := int64(binary.LittleEndian.Uint32(header[8:]))
	if declared < 12 || declared > c.MaxSourceBytes {
		return empty, failure(generation.FailureIntegrity, false, "Tripo 转换来源 GLB 大小无效")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(io.MultiReader(bytes.NewReader(header), reader), c.MaxSourceBytes+1))
	if err != nil {
		return empty, failure(generation.FailureTransport, true, "读取 Tripo 转换来源失败")
	}
	if size != declared || size > c.MaxSourceBytes || meta.Size >= 0 && meta.Size != size {
		return empty, failure(generation.FailureIntegrity, false, "Tripo 转换来源实际字节与声明不一致")
	}
	return nativeConversionSource{Alias: c.SourceAlias, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
}
