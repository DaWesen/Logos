package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrForbiddenAddress 表示目标地址落在禁止访问的网段（回环、私网、链路本地、元数据地址等）。
	ErrForbiddenAddress = errors.New("target address is not allowed")
	// ErrInvalidURL 表示 URL 本身不合法（协议、凭据、格式）。
	ErrInvalidURL = errors.New("invalid url")
)

const maxURLLength = 2048

// 用户可控 URL 的抓取必须按「连接时实际解析到的 IP」判断，
// 只在字符串层拦 localhost 不够：攻击者可以用自己的域名解析到 127.0.0.1，
// 也可以让第一次解析返回公网 IP、真正连接时返回内网 IP（DNS rebinding）。
var forbiddenNets = mustParseCIDRs(
	"0.0.0.0/8",       // 本网络
	"10.0.0.0/8",      // 私网
	"100.64.0.0/10",   // CGNAT
	"127.0.0.0/8",     // 回环
	"169.254.0.0/16",  // 链路本地 / 云元数据 169.254.169.254
	"172.16.0.0/12",   // 私网
	"192.0.0.0/24",    // IETF 协议分配
	"192.0.2.0/24",    // 文档地址
	"192.168.0.0/16",  // 私网
	"198.18.0.0/15",   // 基准测试
	"198.51.100.0/24", // 文档地址
	"203.0.113.0/24",  // 文档地址
	"224.0.0.0/4",     // 组播
	"240.0.0.0/4",     // 保留
	"::/128",          // 未指定
	"::1/128",         // 回环
	"fc00::/7",        // IPv6 唯一本地地址
	"fe80::/10",       // IPv6 链路本地
	"ff00::/8",        // IPv6 组播
	"2001:db8::/32",   // 文档地址
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("safehttp: invalid CIDR " + cidr)
		}
		nets = append(nets, n)
	}
	return nets
}

// IsForbiddenIP 判断 IP 是否禁止用户可控 URL 访问。
// nil 也按禁止处理：宁可拒绝，也不要放行一个无法判断的地址。
func IsForbiddenIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range forbiddenNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateURL 做连接前的快速校验：协议、凭据、主机名与字面量 IP。
// 真正的 DNS 解析与 IP 校验在 DialContext 里再做一次，覆盖 DNS rebinding。
func ValidateURL(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > maxURLLength {
		return nil, fmt.Errorf("%w: length must be 1..%d", ErrInvalidURL, maxURLLength)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme %q", ErrInvalidURL, u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: credentials in url are not allowed", ErrInvalidURL)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidURL)
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p <= 0 || p > 65535 {
			return nil, fmt.Errorf("%w: invalid port", ErrInvalidURL)
		}
	}

	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") ||
		host == "metadata.google.internal" {
		return nil, fmt.Errorf("%w: internal hostname %q", ErrForbiddenAddress, host)
	}

	if ip := net.ParseIP(host); ip != nil && IsForbiddenIP(ip) {
		return nil, fmt.Errorf("%w: %s", ErrForbiddenAddress, ip)
	}
	return u, nil
}

// NewClient 创建只允许访问公网 HTTP(S) 的客户端。
//
// 关键点：DialContext 先解析、校验全部 IP，再直接拨其中一个 IP。
// 校验和拨号使用同一份解析结果，避免「校验时公网、拨号时内网」的 TOCTOU。
func NewClient(timeout time.Duration, maxRedirects int) *http.Client {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if maxRedirects <= 0 {
		maxRedirects = 5
	}

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		// 用户可控 URL 不走环境代理：代理可能把内网地址重新暴露出来。
		Proxy:                 nil,
		DialContext:           safeDialContext(dialer),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   2,
	}

	return &http.Client{
		Transport:     transport,
		Timeout:       timeout,
		CheckRedirect: CheckRedirect(maxRedirects),
	}
}

func safeDialContext(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
		}

		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("%w: resolve %q: %v", ErrForbiddenAddress, host, err)
		}
		for _, ip := range ips {
			if IsForbiddenIP(ip.IP) {
				return nil, fmt.Errorf("%w: %s resolves to %s", ErrForbiddenAddress, host, ip.IP)
			}
		}

		var lastErr error
		for _, ip := range ips {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
}

// CheckRedirect 限制跳转次数，并对每一跳重新做 URL 校验。
// 连接层的 IP 校验不会因为重定向而失效，但协议降级/带凭据的 URL 必须在这里拦住。
func CheckRedirect(maxRedirects int) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("%w: too many redirects (%d)", ErrInvalidURL, len(via))
		}
		_, err := ValidateURL(req.URL.String())
		return err
	}
}
