This is just a simple load balancer that uses the Round Robin algorithm to make sure the distribution is equal among multiple processes.
This repo also contains a very easy to use logger instance that I recommend to be used in any project.
Please pay attention to comments.

# How to use it without docker
1. First just start a few instances of test server (copy the one from test/resources/testserver.go) with port 8081, 8082, 8083
2. Start the load balancer by just typing in the terminal 'go run main.go'
3. Run in terminal this command a few times: 'curl http://localhost:8080'. You will see how in the terminal logs the backend port where the request was proxyied.

# How to use it with docker
1. First just start a few instances of test server (copy the one from test/resources/testserver.go) with port 8081, 8082, 8083
2. Build the load balancer by copying the command inside docs/how-to.md or just use the same command from here: 'docker build -f build/Dockerfile -t loadbalancer-go .'
3. Start with docker run -p 8080:8080 loadbalancer-go
4. Run in terminal this command a few times: 'curl http://localhost:8080'. You will see how in the terminal logs the backend port where the request was proxyied.

Please feel free to implement health checks, handle retries on failure, modify headers and let me know for any other idea, I made this because I was bored and wanted to understand how a load balancer works, I know using Round Robin algorithm is not the best option but I want to stick with that for now.

How to run tests:
# Run all tests
go test -v

# Run specific test
go test -run TestServeHTTP -v

# Run benchmarks
go test -bench=. -v

# Run with race detection
go test -race -v

# Generate coverage report
go test -cover -v

Build in C Go: go env -w CGO_ENABLED=1 and then go build -o loadbalancer-go.exe ./main.go
Build simply: go build -o loadbalancer-go.exe ./main.go