GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
SERVICES := legacy proxy

build: $(addprefix build-,$(SERVICES))

build-%:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags="-s -w" -o bin/$* ./cmd/$*

docker-build:
	docker build -t "aws-sandbox-proxy:latest" .

PHONY: fmt
fmt:
	go fmt ./...
