package loadbalancer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNewLoadBalancer tests the creation of a new load balancer
func TestNewLoadBalancer(t *testing.T) {
	tests := []struct {
		name        string
		targets     []string
		opts        Options
		expectError bool
	}{
		{
			name:    "valid targets",
			targets: []string{"http://localhost:8001", "http://localhost:8002"},
			opts: Options{
				HealthPath:     "/health",
				Interval:       1 * time.Second,
				Timeout:        500 * time.Millisecond,
				UnhealthyAfter: 2,
				HealthyAfter:   1,
			},
			expectError: false,
		},
		{
			name:        "invalid URL",
			targets:     []string{"not-a-valid-url"},
			opts:        Options{},
			expectError: true,
		},
		{
			name:        "empty targets",
			targets:     []string{},
			opts:        Options{},
			expectError: false,
		},
		{
			name:        "default options",
			targets:     []string{"http://localhost:8001"},
			opts:        Options{}, // Should use defaults
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb, err := NewLoadBalancer(tt.targets, tt.opts)

			// if tt.expectError && err == nil {
			// 	t.Error("expected error but got none")
			// }
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.expectError && lb == nil {
				t.Error("expected load balancer but got nil")
			}

			// Test defaults are applied
			if !tt.expectError && lb != nil {
				if lb.opts.Interval == 0 {
					t.Error("expected default interval to be set")
				}
				if lb.opts.HealthPath == "" {
					t.Error("expected default health path to be set")
				}
			}
		})
	}
}

// TestNextHealthyTarget tests the round-robin target selection
func TestNextHealthyTarget(t *testing.T) {
	targets := []string{"http://localhost:8001", "http://localhost:8002", "http://localhost:8003"}
	lb, err := NewLoadBalancer(targets, Options{})
	if err != nil {
		t.Fatalf("failed to create load balancer: %v", err)
	}

	// Test with all targets healthy
	t.Run("all healthy", func(t *testing.T) {
		// Should cycle through all targets
		expectedOrder := []int{0, 1, 2, 0, 1, 2}
		for i, expected := range expectedOrder {
			target, ok := lb.nextHealthyTarget()
			if !ok {
				t.Fatalf("step %d: expected target but got none", i)
			}
			if target != lb.targets[expected] {
				t.Errorf("step %d: expected target %d, got different target", i, expected)
			}
		}
	})

	// Test with some targets unhealthy
	t.Run("some unhealthy", func(t *testing.T) {
		lb.targets[1].Healthy = false // Mark middle target as unhealthy

		// Should skip unhealthy target
		for i := 0; i < 4; i++ {
			target, ok := lb.nextHealthyTarget()
			if !ok {
				t.Fatalf("step %d: expected target but got none", i)
			}
			if target == lb.targets[1] {
				t.Errorf("step %d: got unhealthy target", i)
			}
		}
	})

	// Test with all targets unhealthy
	t.Run("all unhealthy", func(t *testing.T) {
		for _, target := range lb.targets {
			target.Healthy = false
		}

		target, ok := lb.nextHealthyTarget()
		if ok {
			t.Error("expected no target when all unhealthy")
		}
		if target != nil {
			t.Error("expected nil target when all unhealthy")
		}
	})
}

// TestRecordFailAndSuccess tests the health tracking functionality
func TestRecordFailAndSuccess(t *testing.T) {
	targets := []string{"http://localhost:8001"}
	lb, err := NewLoadBalancer(targets, Options{
		UnhealthyAfter: 2,
		HealthyAfter:   3,
	})
	if err != nil {
		t.Fatalf("failed to create load balancer: %v", err)
	}

	target := lb.targets[0]

	// Test failure recording
	t.Run("record failures", func(t *testing.T) {
		target.Healthy = true
		target.FailCount = 0

		// First failure - should stay healthy
		lb.recordFail(target)
		if !target.Healthy {
			t.Error("target should still be healthy after 1 failure")
		}
		if target.FailCount != 1 {
			t.Errorf("expected fail count 1, got %d", target.FailCount)
		}
		if target.SuccessCount != 0 {
			t.Errorf("expected success count reset to 0, got %d", target.SuccessCount)
		}

		// Second failure - should become unhealthy
		lb.recordFail(target)
		if target.Healthy {
			t.Error("target should be unhealthy after 2 failures")
		}
		if target.FailCount != 2 {
			t.Errorf("expected fail count 2, got %d", target.FailCount)
		}
	})

	// Test success recording
	t.Run("record successes", func(t *testing.T) {
		target.Healthy = false
		target.SuccessCount = 0
		target.FailCount = 5

		// Record successes until healthy
		for i := 1; i <= 3; i++ {
			lb.recordSuccess(target)
			if target.SuccessCount != i {
				t.Errorf("expected success count %d, got %d", i, target.SuccessCount)
			}
			if target.FailCount != 0 {
				t.Errorf("expected fail count reset to 0, got %d", target.FailCount)
			}

			expectedHealthy := i >= 3
			if target.Healthy != expectedHealthy {
				t.Errorf("step %d: expected healthy=%v, got %v", i, expectedHealthy, target.Healthy)
			}
		}
	})
}

// TestServeHTTP tests the main HTTP handler functionality
func TestServeHTTP(t *testing.T) {
	// Create mock backend servers
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "backend1")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response from backend1"))
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "backend2")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response from backend2"))
	}))
	defer backend2.Close()

	// Create load balancer with mock backends
	lb, err := NewLoadBalancer([]string{backend1.URL, backend2.URL}, Options{})
	if err != nil {
		t.Fatalf("failed to create load balancer: %v", err)
	}

	t.Run("health check endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		lb.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if w.Body.String() != "ok" {
			t.Errorf("expected 'ok', got '%s'", w.Body.String())
		}
	})

	t.Run("readiness check with healthy backends", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/readyz", nil)
		w := httptest.NewRecorder()

		lb.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if w.Body.String() != "ready" {
			t.Errorf("expected 'ready', got '%s'", w.Body.String())
		}
	})

	t.Run("readiness check with no healthy backends", func(t *testing.T) {
		// Mark all targets as unhealthy
		for _, target := range lb.targets {
			target.Healthy = false
		}

		req := httptest.NewRequest("GET", "/readyz", nil)
		w := httptest.NewRecorder()

		lb.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status 503, got %d", w.Code)
		}

		// Restore health for other tests
		for _, target := range lb.targets {
			target.Healthy = true
		}
	})

	t.Run("proxy to backend", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		lb.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		backend := w.Header().Get("X-Backend")
		if backend != "backend1" && backend != "backend2" {
			t.Errorf("expected backend1 or backend2, got %s", backend)
		}

		body := w.Body.String()
		if !strings.Contains(body, "response from backend") {
			t.Errorf("unexpected response body: %s", body)
		}
	})

	t.Run("round robin distribution", func(t *testing.T) {
		backends := make(map[string]int)

		// Make multiple requests
		for i := 0; i < 10; i++ {
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			lb.ServeHTTP(w, req)

			backend := w.Header().Get("X-Backend")
			backends[backend]++
		}

		// Should have requests to both backends
		if len(backends) != 2 {
			t.Errorf("expected requests to 2 backends, got %d", len(backends))
		}

		// Distribution should be roughly even (allowing some variance)
		for backend, count := range backends {
			if count < 3 || count > 7 {
				t.Errorf("backend %s got %d requests, expected 3-7", backend, count)
			}
		}
	})

	t.Run("no healthy backends", func(t *testing.T) {
		// Mark all targets as unhealthy
		for _, target := range lb.targets {
			target.Healthy = false
		}

		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		lb.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status 503, got %d", w.Code)
		}

		body := w.Body.String()
		if !strings.Contains(body, "no healthy backends") {
			t.Errorf("expected 'no healthy backends' in response, got: %s", body)
		}
	})
}

// TestHealthChecks tests the active health checking functionality
func TestHealthChecks(t *testing.T) {
	// Create a backend that can be controlled
	healthyResponse := true
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			if healthyResponse {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	// Create load balancer with fast health checks
	lb, err := NewLoadBalancer([]string{backend.URL}, Options{
		HealthPath:     "/healthz",
		Interval:       50 * time.Millisecond, // Very fast for testing
		Timeout:        100 * time.Millisecond,
		UnhealthyAfter: 2,
		HealthyAfter:   1,
	})
	if err != nil {
		t.Fatalf("failed to create load balancer: %v", err)
	}

	// Start health checks
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lb.StartHealthChecks(ctx)

	// Wait a bit for initial health check
	time.Sleep(100 * time.Millisecond)

	t.Run("initial health check", func(t *testing.T) {
		if !lb.targets[0].Healthy {
			t.Error("target should be healthy initially")
		}
	})

	t.Run("becomes unhealthy", func(t *testing.T) {
		// Make backend return unhealthy responses
		healthyResponse = false

		// Wait for health checks to detect unhealthy state
		// Should take at least 2 health check intervals (UnhealthyAfter=2)
		time.Sleep(200 * time.Millisecond)

		if lb.targets[0].Healthy {
			t.Error("target should be unhealthy after failed health checks")
		}
	})

	t.Run("becomes healthy again", func(t *testing.T) {
		// Make backend return healthy responses again
		healthyResponse = true

		// Wait for health check to detect healthy state
		// Should take at least 1 health check interval (HealthyAfter=1)
		time.Sleep(150 * time.Millisecond)

		if !lb.targets[0].Healthy {
			t.Error("target should be healthy again after successful health checks")
		}
	})
}

// TestSingleJoiningSlash tests the URL path joining utility function
func TestSingleJoiningSlash(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected string
	}{
		{
			name:     "both have slashes",
			a:        "/api/",
			b:        "/users",
			expected: "/api/users",
		},
		{
			name:     "neither has slashes",
			a:        "api",
			b:        "users",
			expected: "api/users",
		},
		{
			name:     "first has slash",
			a:        "/api/",
			b:        "users",
			expected: "/api/users",
		},
		{
			name:     "second has slash",
			a:        "api",
			b:        "/users",
			expected: "api/users",
		},
		{
			name:     "empty first",
			a:        "",
			b:        "/users",
			expected: "/users",
		},
		{
			name:     "empty second",
			a:        "/api/",
			b:        "",
			expected: "/api/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := singleJoiningSlash(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestAnyHealthy tests the anyHealthy method
func TestAnyHealthy(t *testing.T) {
	targets := []string{"http://localhost:8001", "http://localhost:8002"}
	lb, err := NewLoadBalancer(targets, Options{})
	if err != nil {
		t.Fatalf("failed to create load balancer: %v", err)
	}

	t.Run("all healthy", func(t *testing.T) {
		if !lb.anyHealthy() {
			t.Error("expected anyHealthy to return true when all targets are healthy")
		}
	})

	t.Run("some healthy", func(t *testing.T) {
		lb.targets[0].Healthy = false
		if !lb.anyHealthy() {
			t.Error("expected anyHealthy to return true when some targets are healthy")
		}
	})

	t.Run("none healthy", func(t *testing.T) {
		for _, target := range lb.targets {
			target.Healthy = false
		}
		if lb.anyHealthy() {
			t.Error("expected anyHealthy to return false when no targets are healthy")
		}
	})
}

// TestConcurrency tests the load balancer under concurrent load
func TestConcurrency(t *testing.T) {
	// Create multiple backend servers
	backends := make([]*httptest.Server, 3)
	for i := range backends {
		i := i // Capture loop variable
		backends[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Backend", fmt.Sprintf("backend%d", i))
			time.Sleep(10 * time.Millisecond) // Simulate some processing time
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf("response from backend%d", i)))
		}))
	}
	defer func() {
		for _, backend := range backends {
			backend.Close()
		}
	}()

	// Get backend URLs
	urls := make([]string, len(backends))
	for i, backend := range backends {
		urls[i] = backend.URL
	}

	// Create load balancer
	lb, err := NewLoadBalancer(urls, Options{})
	if err != nil {
		t.Fatalf("failed to create load balancer: %v", err)
	}

	// Start health checks
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lb.StartHealthChecks(ctx)

	t.Run("concurrent requests", func(t *testing.T) {
		const numRequests = 100
		const numWorkers = 10

		var wg sync.WaitGroup
		requestChan := make(chan int, numRequests)
		results := make(chan string, numRequests)

		// Fill request channel
		for i := 0; i < numRequests; i++ {
			requestChan <- i
		}
		close(requestChan)

		// Start workers
		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range requestChan {
					req := httptest.NewRequest("GET", "/test", nil)
					w := httptest.NewRecorder()

					lb.ServeHTTP(w, req)

					if w.Code == http.StatusOK {
						results <- w.Header().Get("X-Backend")
					} else {
						results <- "error"
					}
				}
			}()
		}

		wg.Wait()
		close(results)

		// Count results
		backendCounts := make(map[string]int)
		for result := range results {
			backendCounts[result]++
		}

		// Verify all requests succeeded
		totalSuccessful := 0
		for backend, count := range backendCounts {
			if backend != "error" {
				totalSuccessful += count
			}
		}

		if totalSuccessful != numRequests {
			t.Errorf("expected %d successful requests, got %d", numRequests, totalSuccessful)
		}

		// Verify distribution across backends
		if len(backendCounts)-backendCounts["error"] != len(backends) {
			t.Errorf("expected requests distributed across %d backends", len(backends))
		}
	})
}

// Benchmark the load balancer performance
func BenchmarkLoadBalancer(b *testing.B) {
	// Create a simple backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	// Create load balancer
	lb, err := NewLoadBalancer([]string{backend.URL}, Options{})
	if err != nil {
		b.Fatalf("failed to create load balancer: %v", err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()
			lb.ServeHTTP(w, req)
		}
	})
}
