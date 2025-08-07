FROM golang:1.24.5-alpine3.21 AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .

RUN go build -o loadbalancer-go ./main.go

FROM debian:bullseye-slim

WORKDIR /app

COPY --from=builder /app/loadbalancer-go .

COPY ./build/config.json ./configs/config.json

EXPOSE 8080

ENTRYPOINT ["./loadbalancer-go"]