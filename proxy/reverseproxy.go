package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

const RetryWithSameBackendTimes int = 1

type RetryTimesWithSameBackendKey struct{}
type BackendIndexKey struct{}

type Backend struct {
	URL          *url.URL
	ReverseProxy *httputil.ReverseProxy
}

type Upstream struct {
	Backends []*Backend
}

func (u *Upstream) AddBackend(backend *Backend) {
	u.Backends = append(u.Backends, backend)
}

// GetRetryTimesFromContext 获取请求在相同的 backend 内已重试的次数
func GetRetryTimesFromContext(r *http.Request) int {
	if retry, ok := r.Context().Value(RetryTimesWithSameBackendKey{}).(int); ok {
		return retry
	}
	return 0
}

// GetBackendIndexFromContext 获取当前请求的 backend 在 upstream 中的索引
func GetBackendIndexFromContext(r *http.Request) int {
	if retry, ok := r.Context().Value(BackendIndexKey{}).(int); ok {
		return retry
	}
	return 0
}

// 对失败的请求基于多个 backend 执行轮训重试策略
func (u *Upstream) RoundRobin(rw http.ResponseWriter, r *http.Request) {
	index := GetBackendIndexFromContext(r)
	if len(u.Backends) <= index {
		return
	}
	u.Backends[index].ReverseProxy.ServeHTTP(rw, r)
}

// 初次接收请求后选择第一个 backend 执行
func (u *Upstream) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	u.Backends[0].ReverseProxy.ServeHTTP(rw, r)
}
