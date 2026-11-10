#!/bin/sh

docker buildx build -t gabay/detour --platform linux/amd64,linux/arm64 --build-arg VERSION=prod --push .
