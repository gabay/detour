# syntax=docker/dockerfile:1
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X detour/internal/docker.Version=${VERSION}" \
      -o /out/detour ./cmd/detour

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/detour /detour

ENV DETOUR_CONFIG=/detour.yaml
EXPOSE 7890
ENTRYPOINT ["/detour"]
