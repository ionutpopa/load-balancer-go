package loadbalancer

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
)

type LoadBalancer struct {
	backends []*url.URL
	mu       sync.Mutex
	current  int
}

func NewLoadBalancer(targets []string) (*LoadBalancer, error) {
	backends := make([]*url.URL, 0, len(targets))
	for _, target := range targets {
		parsed, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		backends = append(backends, parsed)
	}

	return &LoadBalancer{
		backends: backends,
	}, nil
}

func (lb *LoadBalancer) getNextBackend() *url.URL {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	backend := lb.backends[lb.current]
	lb.current = (lb.current + 1) % len(lb.backends)
	return backend
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := lb.getNextBackend()
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ServeHTTP(w, r)
}
