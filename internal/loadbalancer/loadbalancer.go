package loadbalancer

import (
	"context"
	"io"
	"load-balancer-go/logger"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Target represents a backend server that can receive proxied requests
type Target struct {
	URL          *url.URL // The backend server URL
	Healthy      bool     // Current health status
	FailCount    int      // Consecutive failure count
	SuccessCount int      // Consecutive success count
}

// Options configures the load balancer's health checking behavior
type Options struct {
	HealthPath     string        // Health check endpoint path (e.g. "/healthz")
	Interval       time.Duration // Time between health checks (e.g. 2 * time.Second)
	Timeout        time.Duration // HTTP timeout for health checks (e.g. 800 * time.Millisecond)
	UnhealthyAfter int           // Mark unhealthy after this many consecutive failures (e.g. 3)
	HealthyAfter   int           // Mark healthy after this many consecutive successes (e.g. 2)
}

// LoadBalancer manages multiple backend targets with health checking and round-robin distribution
type LoadBalancer struct {
	mu      sync.RWMutex           // Protects concurrent access to targets and next index
	targets []*Target              // List of backend targets
	next    int                    // Index for round-robin selection
	client  *http.Client           // HTTP client for health checks
	opts    Options                // Configuration options
	proxy   *httputil.ReverseProxy // Reusable reverse proxy instance
}

// ctxKeyChosenTarget is used as a context key to store the chosen target for a request
type ctxKeyChosenTarget struct{}

// StartHealthChecks begins periodic health checking of all targets in a separate goroutine
// The health checks continue until the provided context is cancelled
func (lb *LoadBalancer) StartHealthChecks(ctx context.Context) {
	ticker := time.NewTicker(lb.opts.Interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Perform health checks on all targets
				lb.checkAllOnce()
			case <-ctx.Done():
				// Context cancelled, stop health checking
				logger.Info("Health checks stopped due to context cancellation")
				return
			}
		}
	}()
}

// checkAllOnce performs health checks on all targets concurrently
// This is called periodically by the health check goroutine
func (lb *LoadBalancer) checkAllOnce() {
	logger.Debug("Starting health check cycle for %d targets", len(lb.targets))
	start := time.Now()
	// Take a snapshot of targets and health path under read lock
	lb.mu.RLock()
	targets := append([]*Target(nil), lb.targets...) // Create copy to avoid holding lock during checks
	path := lb.opts.HealthPath
	lb.mu.RUnlock()

	// Check all targets concurrently using goroutines
	var wg sync.WaitGroup
	wg.Add(len(targets))
	for _, t := range targets {
		go func(t *Target) {
			defer wg.Done()

			// Build health check URL by combining target URL with health path
			u := *t.URL
			u.Path = path
			req, errNewRequest := http.NewRequest(http.MethodGet, u.String(), nil)
			if errNewRequest != nil {
				logger.Error("Failed to create health check request for %s: %v", t.URL.String(), errNewRequest)
			}
			req.Header.Set("User-Agent", "go-lb-healthcheck/1.0")

			// Perform the health check request
			resp, err := lb.client.Do(req)
			if err != nil {
				logger.Debug("Health check failed for %s: %v", t.URL.String(), err)
				// Network error or timeout
				lb.recordFail(t)
				return
			}

			// Drain response body and close it to reuse connection
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			// Check if response indicates healthy status (2xx status codes)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				logger.Debug("Health check successful for %s (status: %d)", t.URL.String(), resp.StatusCode)
				lb.recordSuccess(t)
			} else {
				logger.Debug("Health check failed for %s (status: %d)", t.URL.String(), resp.StatusCode)
				lb.recordFail(t)
			}
		}(t)
	}
	wg.Wait() // Wait for all health checks to complete

	duration := time.Since(start)
	logger.Debug("Health check cycle completed in %v", duration)
}

// recordFail increments the failure count for a target and marks it unhealthy if threshold is reached
func (lb *LoadBalancer) recordFail(t *Target) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	wasHealthy := t.Healthy
	t.FailCount++
	t.SuccessCount = 0 // Reset success count on any failure

	// Mark target as unhealthy if failure threshold is reached
	if t.FailCount >= lb.opts.UnhealthyAfter {
		t.Healthy = false

		// Log when a target becomes unhealthy
		if wasHealthy {
			logger.Info("Target %s marked as UNHEALTHY after %d consecutive failures", t.URL.String(), t.FailCount)
		}
	} else {
		logger.Debug("Target %s failure count: %d/%d", t.URL.String(), t.FailCount, lb.opts.UnhealthyAfter)
	}
}

// recordSuccess increments the success count for a target and marks it healthy if threshold is reached
func (lb *LoadBalancer) recordSuccess(t *Target) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	wasHealthy := t.Healthy
	t.SuccessCount++
	t.FailCount = 0 // Reset failure count on any success

	// Mark target as healthy if success threshold is reached
	if t.SuccessCount >= lb.opts.HealthyAfter {
		t.Healthy = true

		// Log when a target becomes healthy
		if !wasHealthy {
			logger.Info("Target %s marked as HEALTHY after %d consecutive successes", t.URL.String(), t.SuccessCount)
		}
	} else if !t.Healthy {
		logger.Debug("Target %s success count: %d/%d (still unhealthy)", t.URL.String(), t.SuccessCount, lb.opts.HealthyAfter)
	}
}

// NewLoadBalancer creates a new load balancer instance with the given targets and options
// It parses target URLs, applies default values for missing options, and sets up the HTTP client
func NewLoadBalancer(rawTargets []string, opts Options) (*LoadBalancer, error) {
	logger.Info("Creating new load balancer with %d targets", len(rawTargets))
	// Apply default values for missing configuration
	if opts.Interval == 0 {
		opts.Interval = 2 * time.Second
		logger.Debug("Using default health check interval: %v", opts.Interval)
	}
	if opts.Timeout == 0 {
		opts.Timeout = 800 * time.Millisecond
		logger.Debug("Using default health check timeout: %v", opts.Timeout)
	}
	if opts.UnhealthyAfter == 0 {
		opts.UnhealthyAfter = 3
		logger.Debug("Using default unhealthy threshold: %d", opts.UnhealthyAfter)
	}
	if opts.HealthyAfter == 0 {
		opts.HealthyAfter = 2
		logger.Debug("Using default healthy threshold: %d", opts.HealthyAfter)
	}
	if opts.HealthPath == "" {
		opts.HealthPath = "/healthz"
		logger.Debug("Using default health check path: %s", opts.HealthPath)
	}

	// Parse target URLs and create Target instances
	ts := make([]*Target, 0, len(rawTargets))
	for i, t := range rawTargets {
		u, err := url.Parse(t)
		if err != nil {
			logger.Error("Failed to parse target URL '%s': %v", t, err)
			return nil, err // Invalid URL format
		}
		// Start with all targets marked as healthy
		ts = append(ts, &Target{URL: u, Healthy: true})

		logger.Info("Added target %d: %s (initially healthy)", i+1, u.String())
	}

	// Create load balancer instance
	lb := &LoadBalancer{
		targets: ts,
		client: &http.Client{
			Timeout: opts.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100, // Maximum idle connections across all hosts
				MaxIdleConnsPerHost: 10,  // Maximum idle connections per host
			},
		},
		opts: opts,
	}

	logger.Info("Load balancer created successfully with %d targets", len(ts))
	logger.Debug("Health check configuration - Path: %s, Interval: %v, Timeout: %v, UnhealthyAfter: %d, HealthyAfter: %d",
		opts.HealthPath, opts.Interval, opts.Timeout, opts.UnhealthyAfter, opts.HealthyAfter)

	// Create a reusable reverse proxy instance
	// The Director and ErrorHandler will be customized per request
	lb.proxy = &httputil.ReverseProxy{
		Director:       func(r *http.Request) {}, // Will be overridden per request
		ModifyResponse: func(resp *http.Response) error { return nil },
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("Proxy error: %v", err)
			// Mark target as failed on proxy errors (passive health checking)
			lb.markCurrentTargetFailed(r)
			http.Error(w, "upstream error", http.StatusBadGateway)
		},
	}
	return lb, nil
}

// nextHealthyTarget selects the next healthy target using round-robin algorithm
// Returns the target and true if found, or nil and false if no healthy targets exist
func (lb *LoadBalancer) nextHealthyTarget() (*Target, bool) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if len(lb.targets) == 0 {
		logger.Error("No targets configured in load balancer")
		return nil, false
	}

	// Count healthy targets for logging
	healthyCount := 0
	for _, target := range lb.targets {
		if target.Healthy {
			healthyCount++
		}
	}

	if healthyCount == 0 {
		logger.Error("No healthy targets available out of %d total targets", len(lb.targets))
		return nil, false
	}

	// Try up to N times to find a healthy target (where N is the number of targets)
	n := len(lb.targets)
	start := lb.next
	for i := range n {
		idx := (start + i) % n // Calculate index with wraparound
		if lb.targets[idx].Healthy {
			// Move pointer to next target for subsequent requests
			lb.next = (idx + 1) % n

			logger.Debug("Selected target %s for request (healthy targets: %d/%d)",
				lb.targets[idx].URL.String(), healthyCount, len(lb.targets))
			return lb.targets[idx], true
		}
	}

	// This shouldn't happen if healthyCount > 0, but just in case
	logger.Error("Failed to find healthy target despite %d healthy targets reported", healthyCount)
	return nil, false
}

// ServeHTTP implements http.Handler interface, making LoadBalancer usable as an HTTP server
// It handles health check endpoints and proxies other requests to healthy backend targets
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Log incoming requests (debug level to avoid spam)
	logger.Debug("Incoming request: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	// Handle load balancer's own health check endpoint
	if r.URL.Path == "/healthz" {
		logger.Debug("Serving load balancer health check")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// Handle readiness check endpoint (returns healthy only if backends are available)
	if r.URL.Path == "/readyz" {
		if lb.anyHealthy() {
			logger.Debug("Readiness check passed - healthy backends available")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ready"))
			return
		} else {
			logger.Info("Readiness check failed - no healthy backends available")
			http.Error(w, "no healthy backends", http.StatusServiceUnavailable)
			return
		}
	}

	// Proxy request to a healthy target
	tried := 0
	for tried < len(lb.targets) {
		// Select next healthy target
		tgt, ok := lb.nextHealthyTarget()
		if !ok {
			// No healthy targets available
			logger.Error("Request failed - no healthy backends available for %s %s", r.Method, r.URL.Path)
			http.Error(w, "no healthy backends", http.StatusServiceUnavailable)
			return
		}

		logger.Debug("Proxying request %s %s to target %s", r.Method, r.URL.Path, tgt.URL.String())

		// Create a new reverse proxy for this specific target
		proxy := httputil.NewSingleHostReverseProxy(tgt.URL)

		// Customize the proxy's Director to preserve the original request path and query
		origDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			origDirector(req) // Sets scheme and host from target URL
			// Preserve the incoming request's path and query parameters
			req.URL.Path = singleJoiningSlash(tgt.URL.Path, r.URL.Path)
			req.URL.RawQuery = r.URL.RawQuery
			req.Host = tgt.URL.Host

			logger.Debug("Proxying to final URL: %s", req.URL.String())
		}

		// Handle proxy errors (passive health checking)
		proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
			logger.Error("Proxy error for target %s: %v", tgt.URL.String(), err)
			lb.recordFail(tgt) // Mark this target as failed
			http.Error(w, "upstream error", http.StatusBadGateway)
		}

		// Execute the proxy request
		proxy.ServeHTTP(w, r)

		logger.Debug("Request %s %s completed successfully via target %s", r.Method, r.URL.Path, tgt.URL.String())
		return // Request completed successfully
	}
}

// markCurrentTargetFailed marks the target stored in the request context as failed
// This is used for passive health checking when proxy errors occur
func (lb *LoadBalancer) markCurrentTargetFailed(r *http.Request) {
	v := r.Context().Value(ctxKeyChosenTarget{})
	if tgt, ok := v.(*Target); ok {
		logger.Info("Marking target %s as failed due to proxy error", tgt.URL.String())
		lb.recordFail(tgt)
	} else {
		logger.Debug("No target found in request context for passive health check")
	}
}

// anyHealthy returns true if at least one target is currently marked as healthy
// This is used by the readiness check endpoint
func (lb *LoadBalancer) anyHealthy() bool {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	healthyCount := 0
	for _, t := range lb.targets {
		if t.Healthy {
			healthyCount++
		}
	}

	if healthyCount == 0 {
		logger.Debug("No healthy targets found out of %d total targets", len(lb.targets))
	}

	return healthyCount > 0
}

// singleJoiningSlash intelligently joins two URL paths with exactly one slash between them
// It handles cases where either path may or may not have leading/trailing slashes
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		// Both have slashes: remove one
		return a + b[1:]
	case !aslash && !bslash:
		// Neither has slashes: add one
		return a + "/" + b
	default:
		// Exactly one has a slash: join as-is
		return a + b
	}
}

// TargetStatus represents the status of a single target for external monitoring
type TargetStatus struct {
	URL          string `json:"url"`
	Healthy      bool   `json:"healthy"`
	FailCount    int    `json:"fail_count"`
	SuccessCount int    `json:"success_count"`
}

// GetTargetStatus returns the current status of all targets for monitoring/debugging
func (lb *LoadBalancer) GetTargetStatus() []TargetStatus {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	status := make([]TargetStatus, len(lb.targets))
	for i, target := range lb.targets {
		status[i] = TargetStatus{
			URL:          target.URL.String(),
			Healthy:      target.Healthy,
			FailCount:    target.FailCount,
			SuccessCount: target.SuccessCount,
		}
	}

	logger.Debug("Target status requested - returning status for %d targets", len(status))

	return status
}

func (lb *LoadBalancer) StartStatusLogging(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				status := lb.GetTargetStatus()
				healthy := 0
				for _, s := range status {
					if s.Healthy {
						healthy++
					}
				}
				logger.Info("Target status summary: %d/%d healthy targets", healthy, len(status))

				// Log details for unhealthy targets
				for _, s := range status {
					if !s.Healthy {
						logger.Info("Unhealthy target: %s (failures: %d)", s.URL, s.FailCount)
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}
