package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"load-balancer-go/internal/loadbalancer"
	"load-balancer-go/logger"
	"load-balancer-go/types"
	"log"
	"net/http"
	"os"
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
	if err := logger.InitLogger("logs"); err != nil {
		panic(fmt.Sprintf("could not initialize logger: %v", err))
	}

	config, loadConfigErr := loadConfig("./configs/config.json")

	if loadConfigErr != nil {
		logger.Error("Error when loading config.json: %v", loadConfigErr)
	}

	toLogConfig, toLogConfigErr := printConfigAsJSON(config)

	if toLogConfigErr != nil {
		logger.Error("Error when pretty printing config.json: %v", toLogConfigErr)
	}

	logger.Info("Load Balancer Started")
	logger.Info("Config content: %v", toLogConfig)

	lb, err := loadbalancer.NewLoadBalancer(config.Targets)
	if err != nil {
		logger.Error("Failed to create load balancer: %v", err)
		return
	}

	port := ":8080"
	logger.Info("Listening on %s", port)
	if err := http.ListenAndServe(port, lb); err != nil {
		logger.Error("Server failed: %v", err)
	}
}
