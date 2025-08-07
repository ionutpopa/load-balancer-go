# Load Balancer: High-Level Functionality

1. Accept Requests:
    - The load balancer listens on a public port (e.g., :80 or :443).
    - Clients send HTTP requests to it (thinking it's the main server).
2. Choose a Backend Server:
    - It decides which backend server should handle the request using a load balancing algorithm (see below).
3. Forward the Request:
   - It forwards the client request to the selected backend (e.g., via HTTP reverse proxy).
   - It may (not implemented yet):
     - Modify headers (e.g., add X-Forwarded-For)
     - Keep TCP connections alive
     - Handle retries on failure
4. Return the Response:
    - It receives the response from the backend and sends it back to the client.