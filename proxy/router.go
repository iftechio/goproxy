package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/goproxyio/goproxy/v2/renameio"
	"github.com/goproxyio/goproxy/v2/sumdb"
)

// ListExpire list data expire data duration.
const ListExpire = 5 * time.Minute

// RouterOptions provides the proxy host and the external pattern
type RouterOptions struct {
	Pattern      string
	Proxies      []*url.URL
	DownloadRoot string
}

// A Router is the proxy HTTP server,
// which implements Route Filter to
// routing private module or public module .
type Router struct {
	srv           *Server
	proxyUpstream *Upstream
	pattern       string
	downloadRoot  string
}

// NewRouter returns a new Router using the given operations.
func NewRouter(srv *Server, opts *RouterOptions) *Router {
	rt := &Router{
		srv: srv,
	}
	upstream := &Upstream{}
	if opts != nil {
		if len(opts.Proxies) == 0 {
			log.Printf("not set proxy, all direct.")
			return rt
		}
		for _, url := range opts.Proxies {
			url := url
			proxy := httputil.NewSingleHostReverseProxy(url)

			upstream.AddBackend(&Backend{
				URL:          url,
				ReverseProxy: proxy,
			})

			director := proxy.Director
			proxy.Director = func(r *http.Request) {
				director(r)
				r.Host = url.Host
			}

			proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, err error) {
				retries := GetRetryTimesFromContext(r)
				if retries == 0 {
					log.Printf("%s proxy error: %s, will try to retry %d times\n", r.Host, err.Error(), RetryWithSameBackendTimes)
				}
				if retries < RetryWithSameBackendTimes {
					<-time.After(100 * time.Millisecond)
					ctx := context.WithValue(r.Context(), RetryTimesWithSameBackendKey{}, retries+1)
					proxy.ServeHTTP(rw, r.WithContext(ctx))
					return
				}
				// 如果重试后仍然不成功，继续尝试将请求发往剩下的 backend
				backendIndex := GetBackendIndexFromContext(r)
				backendIndex = backendIndex + 1
				ctx := context.WithValue(r.Context(), BackendIndexKey{}, backendIndex)
				log.Printf("%s proxy error: %s, the request still fails after retrying, will send request to next backend, index: %d\n", r.Host, err.Error(), backendIndex)
				upstream.RoundRobin(rw, r.WithContext(ctx))
			}

			proxy.Transport = &http.Transport{
				Proxy:           http.ProxyFromEnvironment,
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}

			proxy.ModifyResponse = func(r *http.Response) error {
				log.Printf("upstream proxy: %d %s", r.StatusCode, r.Request.URL)
				if r.StatusCode == http.StatusOK {
					var buf []byte
					var err error
					if strings.Contains(r.Header.Get("Content-Encoding"), "gzip") {
						gr, err := gzip.NewReader(r.Body)
						if err != nil {
							return err
						}
						defer gr.Close()
						buf, err = ioutil.ReadAll(gr)
						if err != nil {
							return err
						}
						r.Header.Del("Content-Encoding")
						decompressedBodyLength := strconv.Itoa(len(buf))
						r.Header.Set("Content-Length", decompressedBodyLength)
					} else {
						buf, err = ioutil.ReadAll(r.Body)
						if err != nil {
							return err
						}
					}
					r.Body = ioutil.NopCloser(bytes.NewReader(buf))
					if buf != nil {
						file := filepath.Join(opts.DownloadRoot, r.Request.URL.Path)
						os.MkdirAll(path.Dir(file), os.ModePerm)
						err = renameio.WriteFile(file, buf, 0666)
						if err != nil {
							return err
						}
					}
				}

				// support 302 status code.
				if r.StatusCode == http.StatusFound {
					loc := r.Header.Get("Location")
					if loc == "" {
						return fmt.Errorf("%d response missing Location header", r.StatusCode)
					}

					// TODO: location is relative.
					_, err := url.Parse(loc)
					if err != nil {
						return fmt.Errorf("failed to parse Location header %q: %v", loc, err)
					}
					resp, err := http.Get(loc)
					if err != nil {
						return err
					}
					defer resp.Body.Close()

					var buf []byte
					if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
						gr, err := gzip.NewReader(resp.Body)
						if err != nil {
							return err
						}
						defer gr.Close()
						buf, err = ioutil.ReadAll(gr)
						if err != nil {
							return err
						}
						resp.Header.Del("Content-Encoding")
					} else {
						buf, err = ioutil.ReadAll(resp.Body)
						if err != nil {
							return err
						}
					}
					resp.Body = ioutil.NopCloser(bytes.NewReader(buf))
					if buf != nil {
						file := filepath.Join(opts.DownloadRoot, r.Request.URL.Path)
						os.MkdirAll(path.Dir(file), os.ModePerm)
						err = renameio.WriteFile(file, buf, 0666)
						if err != nil {
							return err
						}
					}
				}
				// // 此段代码用来测试重试逻辑
				// if r.StatusCode == http.StatusNotFound {
				// 	return errors.New("not found")
				// }
				return nil
			}
		}

		rt.proxyUpstream = upstream
		rt.pattern = opts.Pattern
		rt.downloadRoot = opts.DownloadRoot
	}
	return rt
}

// Direct decides whether a path should directly access.
func (rt *Router) Direct(path string) bool {
	if rt.pattern == "" {
		return false
	}
	return GlobsMatchPath(rt.pattern, path)
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// sumdb handler
	if strings.HasPrefix(r.URL.Path, "/sumdb/") {
		sumdb.Handler(w, r)
		return
	}

	if len(rt.proxyUpstream.Backends) == 0 || rt.Direct(strings.TrimPrefix(r.URL.Path, "/")) {
		log.Printf("------ --- %s [direct]\n", r.URL)
		rt.srv.ServeHTTP(w, r)
		return
	}

	file := filepath.Join(rt.downloadRoot, r.URL.Path)
	if info, err := os.Stat(file); err == nil {
		if f, err := os.Open(file); err == nil {
			var ctype string
			defer f.Close()
			if strings.HasSuffix(r.URL.Path, "/@latest") {
				if time.Since(info.ModTime()) >= ListExpire {
					log.Printf("------ --- %s [proxy]\n", r.URL)
					rt.proxyUpstream.ServeHTTP(w, r)
				} else {
					ctype = "text/plain; charset=UTF-8"
					w.Header().Set("Content-Type", ctype)
					log.Printf("------ --- %s [cached]\n", r.URL)
					http.ServeContent(w, r, "", info.ModTime(), f)
				}
				return
			}

			i := strings.Index(r.URL.Path, "/@v/")
			if i < 0 {
				http.Error(w, "no such path", http.StatusNotFound)
				return
			}

			what := r.URL.Path[i+len("/@v/"):]
			if what == "list" {
				if time.Since(info.ModTime()) >= ListExpire {
					log.Printf("------ --- %s [proxy]\n", r.URL)
					rt.proxyUpstream.ServeHTTP(w, r)
					return
				}
				ctype = "text/plain; charset=UTF-8"
			} else {
				ext := path.Ext(what)
				switch ext {
				case ".info":
					ctype = "application/json"
				case ".mod":
					ctype = "text/plain; charset=UTF-8"
				case ".zip":
					ctype = "application/octet-stream"
				default:
					http.Error(w, "request not recognized", http.StatusNotFound)
					return
				}
			}
			w.Header().Set("Content-Type", ctype)
			log.Printf("------ --- %s [cached]\n", r.URL)
			http.ServeContent(w, r, "", info.ModTime(), f)
			return
		}
	}
	log.Printf("------ --- %s [proxy]\n", r.URL)
	rt.proxyUpstream.ServeHTTP(w, r)
	return
}

// GlobsMatchPath reports whether any path prefix of target
// matches one of the glob patterns (as defined by path.Match)
// in the comma-separated globs list.
// It ignores any empty or malformed patterns in the list.
func GlobsMatchPath(globs, target string) bool {
	for globs != "" {
		// Extract next non-empty glob in comma-separated list.
		var glob string
		if i := strings.Index(globs, ","); i >= 0 {
			glob, globs = globs[:i], globs[i+1:]
		} else {
			glob, globs = globs, ""
		}
		if glob == "" {
			continue
		}

		// A glob with N+1 path elements (N slashes) needs to be matched
		// against the first N+1 path elements of target,
		// which end just before the N+1'th slash.
		n := strings.Count(glob, "/")
		prefix := target
		// Walk target, counting slashes, truncating at the N+1'th slash.
		for i := 0; i < len(target); i++ {
			if target[i] == '/' {
				if n == 0 {
					prefix = target[:i]
					break
				}
				n--
			}
		}
		if n > 0 {
			// Not enough prefix elements.
			continue
		}
		matched, _ := path.Match(glob, prefix)
		if matched {
			return true
		}
	}
	return false
}
