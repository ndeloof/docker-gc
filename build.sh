#!/bin/bash
CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o docker-gc .
docker build -t ndeloof/docker-gc .
