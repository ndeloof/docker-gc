CGO_ENABLED=0
GOOS=linux
GOARCH=amd64
COMMIT=`git rev-parse --short HEAD`
APP=docker-gc
REPO?=ggtools/$(APP)
TAG?=latest

all: image

deps:
	@go get -d -v

build: build-docker

build-app: docker-gc

docker-gc: deps
	@go build -v

build-docker:
	@docker run --rm -v $(PWD):/usr/src/$(APP) -w /usr/src/$(APP) golang bash -c "make build-app"

build-image:
	@docker build -t $(REPO):$(TAG) .
	@echo "Image created: $(REPO):$(TAG)"

image: build build-image

clean:
	@rm docker-gc

.PHONY: deps build build-docker build-app build-image image clean
