How to build the Dockerfile:
- docker build -f build/Dockerfile -t loadbalancer-go .

How to run the container with new load balancer image:
- docker run -p 8080:8080 loadbalancer-go


COMMENT: The bellow part I have copied from another project and I though it would be a good idea to include it here in case someone randomly ends up on my repo and is curious how to mock interface for unit tests

To create mocks from interfaces inside packages: 
- run: brew install mockery
- run: mockery init github.com/nats-io/nats.go (or your package)
- change: .mockery.yml dir to the actual dir where you want the mock to be generated
- run: mockery
- maybe delete .mockery.yml