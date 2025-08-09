package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"load-balancer-go/internal/loadbalancer"
	"load-balancer-go/logger"
	"load-balancer-go/types"
	"log"
	"net/http"
	"os"
	"time"
)

func printConfigAsJSON(config any) (string, error) {
	prettyJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal config: %v\n", err)
		return "", errors.New("error printing the pretified version of object")
	}
	return string(prettyJSON), nil
}

func loadConfig(configPath string) (*types.Config, error) {
	// Check if file exists
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			log.Println("Config file does not exist")
			return nil, err
		}
		log.Printf("Error accessing config file: %v\n", err)
		return nil, err
	}

	// Read file
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("Error reading file: %v\n", err)
		return nil, err
	}

	// Parse into actual struct
	var config *types.Config
	if err := json.Unmarshal(configContent, &config); err != nil {
		log.Fatalf("Error parsing JSON: %v", err)
	}

	return config, nil
}

func main() {
	// Initialize logging system with log directory
	if err := logger.InitLogger("logs"); err != nil {
		panic(fmt.Sprintf("could not initialize logger: %v", err))
	}

	// Load configuration from JSON file
	config, err := loadConfig("./configs/config.json")
	if err != nil {
		logger.Error("Error when loading config.json: %v", err)
	}

	// Convert config to JSON for logging purposes
	toLogConfig, _ := printConfigAsJSON(config)

	logger.Info("Load Balancer Started")
	logger.Info("Config content: %v", toLogConfig)

	// Create new load balancer instance with health check options
	lb, err := loadbalancer.NewLoadBalancer(
		config.Targets, // Array of target server URLs
		loadbalancer.Options{
			HealthPath:     "/healthz",             // Endpoint to check for health
			Interval:       2 * time.Second,        // How often to check health
			Timeout:        800 * time.Millisecond, // Request timeout for health checks
			UnhealthyAfter: 3,                      // Mark unhealthy after 3 consecutive failures
			HealthyAfter:   2,                      // Mark healthy after 2 consecutive successes
		},
	)
	if err != nil {
		logger.Error("Failed to create load balancer: %v", err)
		return
	}

	// Start background health checking with cancellation context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lb.StartHealthChecks(ctx)

	// Start HTTP server on port 8080
	port := ":8080"
	logger.Info("Listening on %s", port)
	if err := http.ListenAndServe(port, lb); err != nil {
		logger.Error("Server failed: %v", err)
	}

	go func() {
		lb.StartStatusLogging(ctx, 30*time.Second) // Log every 30 seconds
	}()
}
