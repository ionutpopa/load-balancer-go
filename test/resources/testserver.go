package resources // This should be replaced with 'package main' as this server should start in another project, this is just for test purposes

import (
	"fmt"
	"net/http"
)

func main() {
	port := "8081" // Change to 8082 and 8083 for others

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Served by backend %s\n", port)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Health check served by backend %s\n", port)
	})

	fmt.Println("Backend running on port", port)
	http.ListenAndServe(":"+port, nil)
}
