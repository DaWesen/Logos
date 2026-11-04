package handler

import (
	"net"
	"net/http"
	"time"
)

const (
	// 普通查询/删除/重处理接口：process 正常在毫秒级返回，30s 足够。
	processAPIRequestTimeout = 30 * time.Second
	// 上传要读完 100MB body，单独给 10 分钟；不能用统一超时误伤正常上传。
	processUploadRequestTimeout = 10 * time.Minute
)

var (
	processAPIClient    = newProcessHTTPClient(processAPIRequestTimeout)
	processUploadClient = newProcessHTTPClient(processUploadRequestTimeout)
)

// newProcessHTTPClient 统一网关到 process 的 HTTP 行为。
//
// 零值 http.Client 没有超时：下游一旦卡住，网关的 goroutine 和连接会一直占着，
// 上游请求取消也无法释放。这里同时设置请求总超时、拨号超时和连接池上限，
// 让慢下游表现为可预期的 5xx，而不是把网关拖垮。
func newProcessHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: newProcessTransport(),
	}
}

func newProcessTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		// 反向代理不读 http.Client.Timeout，必须单独限制等待响应头的时间，
		// 否则下游连上后不返回，网关连接仍会一直挂着。
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
