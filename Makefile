VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X detour/internal/docker.Version=$(VERSION)
IMAGE ?= detour:$(VERSION)

.PHONY: build test vet fmt docker clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o detour ./cmd/detour

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean:
	rm -f detour
