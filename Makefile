# Variables
VENDOR_DIR=vendor
DOCKER_CLIENT_DIR=github.com/fsouza/go-dockerclient
DOCKER_CLIENT_FILE=client.go
MOCK_DOCKER_CLIENT=mock_dockerclient.go

# Directories
GO_OUT_DIR := pb

# Default target
all: clean tests run

build:
	go build

run:
	go run main.go

fmt:
	go fmt ./...

tests:
	go test ./...

# Clean up generated mocks and vendor directory
clean:

