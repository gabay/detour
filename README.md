# Detour

Detour is a lightweight Go HTTP server that acts as a Traefik HTTP provider. It merges static Traefik dynamic configuration read from YAML files with configuration derived from Docker and Docker Swarm labels, and serves the result to Traefik so routers, services, middlewares and TLS settings can be declared either way.

## Architecture

- Config loader: reads the Detour configuration file, including the static dynamic-configuration section.
- Docker/Swarm collector: watches containers and services and decodes their `traefik.*` labels.
- Compiler/merger: turns label-derived configuration into Traefik objects and merges it with the static configuration.
- HTTP server: serves the merged configuration to Traefik's HTTP provider.

## Configuration

Detour reads one YAML file, passed at startup, and reloads it when it changes (see [Config reload](#config-reload)). Unknown keys are an error. See [`config.example.yaml`](config.example.yaml) for a fully commented example with every option.

Durations accept Go duration strings (`"10s"`, `"2m"`) or a plain integer number of seconds (`10`).

### `server`

| Key              | Default  | Description |
|------------------|----------|-------------|
| `listenAddr`     | `:7890`  | Address the HTTP provider endpoint listens on. |
| `endpointPath`   | `/`      | Path Traefik polls. Must start with `/`. |
| `updateInterval` | `10s`    | Polling interval for Docker containers and Swarm services. Must be > 0. |
| `staleDuration`  | `1m`     | Discovered entries not refreshed within this window are dropped. Must be > 0. |

### `docker`

| Key                 | Default                       | Description |
|---------------------|-------------------------------|-------------|
| `containers`        | `true`                        | Collect labels from Docker containers. |
| `swarm`             | `false`                       | Collect labels from Docker Swarm services. |
| `endpoint`          | `unix:///var/run/docker.sock` | Docker API endpoint. Scheme must be `unix`, `tcp`, `http` or `https`. |
| `timeout`           | `5s`                          | Timeout per Docker API request. Must be > 0. |
| `username`          |                               | Basic auth for socket proxies. Set together with `password`. |
| `password`          |                               | Basic auth password. Set together with `username`. |
| `tls.ca`            |                               | CA certificate file for the Docker API. |
| `tls.cert`          |                               | Client certificate file. Set together with `tls.key`. |
| `tls.key`           |                               | Client key file. Set together with `tls.cert`. |
| `tls.insecureSkipVerify` | `false`                  | Skip server certificate verification. |
| `exposedByDefault`  | `false`                       | Expose containers that lack `traefik.enable=true`. |
| `network`           |                               | Network used to reach containers attached to several networks. |
| `defaultRule`       | ``Host(`{{ normalize .Name }}`)`` | Router rule template generated when a container sets none. See [default rule functions](#default-rule-functions). |
| `constraints`       |                               | Only containers matching this Traefik constraint expression are used. |
| `useBindPortIP`     | `false`                       | Use the IP from the published port binding instead of the container IP. |
| `allowEmptyServices`| `false`                       | Keep services with no endpoints instead of dropping them. |

#### Label sources

`containers` and `swarm` are independent booleans that select the label sources. Any combination is valid; with both `false`, Detour publishes only the static `dynamic` section and contacts no Docker API. The keys are strict: a leftover `mode:` key is rejected at startup as an unknown field.

#### Default rule functions

`docker.defaultRule` is a Go `text/template` executed with `.Name`, `.ContainerName` and `.Labels`. Available functions:

| Function | Example | Result |
|----------|---------|--------|
| `normalize` | `{{ normalize "My_App.web" }}` | `My-App-web` |
| `lower` | `{{ lower "App" }}` | `app` |
| `upper` | `{{ upper "app" }}` | `APP` |
| `trimPrefix` | `{{ trimPrefix "x-" "x-app" }}` | `app` |
| `trimSuffix` | `{{ trimSuffix "-x" "app-x" }}` | `app` |
| `replace` | `{{ replace "_" "-" "a_b" }}` | `a-b` |
| `trimProject` | `{{ trimProject "project_container" }}` | `container` |

`trimProject` returns the part after the last underscore, or the input unchanged when there is no underscore or the underscore is the last character. Note that `.Name` for containers carrying compose labels is `<service>_<project>` (service first, project last), so `trimProject` on such a name returns the project, not the service. It is meant for Swarm services and for plain container names such as `project_container`; for compose containers, use `.ContainerName` or `.Labels` instead.

The global `server.updateInterval` drives polling for both containers and Swarm services.

### `dynamic`

Optional. Static Traefik dynamic configuration (`http`, `tcp`, `udp`, `tls` sections) in Traefik's own format. Use it for objects that are not backed by a container, such as a service pointing at an external host. Entries here win over Docker/Swarm entries with the same name.

## Docker labels

Containers and Swarm services control their routing with these labels:

| Label                            | Applies to        | Meaning |
|----------------------------------|-------------------|---------|
| `traefik.enable`                 | containers, swarm | `true`/`false`; overrides `exposedByDefault` |
| `traefik.docker.network`         | containers        | Network used for the backend address |
| `traefik.docker.allownonrunning` | containers        | Keep the container even if it is not running |
| `traefik.swarm.lbswarm`          | swarm             | Route through the service VIP instead of task addresses |
| `traefik.swarm.network`          | swarm             | Network used for the backend address |

For Swarm services, `traefik.docker.lbswarm` and `traefik.docker.network` are accepted as deprecated aliases, but a service must not mix `traefik.docker.*` and `traefik.swarm.*` labels.

`docker.constraints` takes a Traefik-style expression built from `Label(name, value)` and `LabelRegex(name, regex)`, combined with `&&`, `||`, `!` and parentheses. An empty value matches all containers. The expression is validated at startup.

```yaml
docker:
  constraints: 'Label("traefik.constraint", "public") || !LabelRegex("env", "^dev")'
```

## Quick start

```sh
make build                       # produces ./detour
cp config.example.yaml config.yaml
./detour -config config.yaml     # serves on :7890 by default
curl -s http://localhost:7890/   # merged configuration as YAML
```

Detour starts serving immediately. Until the first successful poll it answers `503`, which Traefik treats as "no change".

## HTTP endpoint

Served at `server.endpointPath` (default `/`):

| Request | Result |
|---------|--------|
| `GET /` | YAML, `Content-Type: application/yaml; charset=utf-8` |
| `GET /?format=json` | JSON, `application/json; charset=utf-8` |
| `GET /?format=yaml` | YAML explicitly |
| `GET /` with `Accept: application/json` | JSON (ignored when `format` is set) |
| `GET /?format=xml` | `400 Bad Request` |
| `HEAD` | same headers, no body |
| other methods | `405 Method Not Allowed` with `Allow: GET, HEAD` |
| other paths | `404 Not Found` |

**Compression.** When `Accept-Encoding` allows gzip (an explicit `gzip` or `*` with q > 0, and not `gzip;q=0`), the pre-compressed body is sent with `Content-Encoding: gzip`. `Vary: Accept-Encoding` is always set. Gzip bytes are created once per poll, never per request.

**Caching.** Responses carry a strong `ETag` derived from the uncompressed content (gzip variants have their own ETag). `If-None-Match` returns `304 Not Modified`. `Cache-Control: no-store` is set.

**Staleness and 503.** Detour answers `503 Service Unavailable` with `Retry-After` (the update interval in seconds) when:

- no configuration has been built yet, or
- the last successful build is older than `server.staleDuration`.

The body explains which case applies. Traefik's HTTP provider treats any non-200 response as an error and keeps its previous configuration, so a Docker outage degrades to "no change" for `staleDuration`, after which Traefik keeps its last configuration until Detour recovers. Failed polls keep the previous snapshot in place; they never publish an empty one.

**Health.** `GET /healthz` returns `200 ok` when a fresh configuration exists, otherwise `503` with the same reason text. Use it for container health checks. `/healthz` cannot be used as `endpointPath`.

## Config reload

The configuration file is watched with inotify (via `fsnotify`) on its parent directory, so editors that save by rename, `sed -i`, and Kubernetes ConfigMap or Docker bind mounts are all detected. Events are debounced by 250ms, so a burst of writes causes one reload. Each reload is logged as `configuration reloaded` with the sections that changed.

- **Applied live:** `docker` (the Docker client, collector and builder are rebuilt), `dynamic`, `server.updateInterval`, `server.staleDuration` and `server.endpointPath`. The new configuration is published by an immediate refresh.
- **Requires a restart:** `server.listenAddr`. A change is logged as a warning and the old address keeps being served.
- **Failure:** if the file cannot be read, parsed or validated, or the Docker components cannot be built, an error is logged and the previous configuration keeps running.
- **SIGHUP:** sending `SIGHUP` forces a reload of the file (for example when inotify events are not delivered).
- **Bind mounts:** mount the directory containing the file, or a file whose directory is replaced atomically. A single-file bind mount that is replaced by rename may not produce events; use `SIGHUP` in that case.

## Command-line flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `config.yaml` (or `$DETOUR_CONFIG`) | Configuration file path. |
| `-log-level` | `info` | `debug`, `info`, `warn` or `error`. Request logs are at `debug`. |
| `-log-format` | `text` | `text` or `json`. |
| `-version` | | Print the version and exit. |

Detour exits with status 1 on configuration or startup errors. `SIGINT` and `SIGTERM` trigger a graceful shutdown (5 second limit).

## Building

```sh
make build        # CGO_ENABLED=0 binary with the version from git describe
make test         # go test -race ./...
make docker       # image tagged detour:<version>
```

The version is injected with `-ldflags "-X detour/internal/docker.Version=<version>"`; the same flag is used by the Dockerfile through the `VERSION` build argument.

## Docker

```sh
docker build --build-arg VERSION=0.1.0 -t detour:0.1.0 .
docker run --rm -p 7890:7890 \
  -v "$PWD/config.yaml:/etc/detour/config.yaml:ro" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  detour:0.1.0
```

The image is `gcr.io/distroless/static:nonroot` and only contains the binary. Mount the config at `/etc/detour/config.yaml`. The Docker socket must be readable by the non-root user, or use a socket proxy (see `docker-compose.example.yaml`, which runs Traefik, Detour and a `tecnativa/docker-socket-proxy` variant).

## Traefik provider

```yaml
# Traefik v3 static configuration
providers:
  http:
    endpoint: "http://detour:7890/"
    pollInterval: 5s
```

Equivalent flags: `--providers.http.endpoint=http://detour:7890/ --providers.http.pollInterval=5s`. Keep Traefik's `pollInterval` at or below Detour's `updateInterval` so Traefik picks up changes promptly.

## Status

| Component                    | Status  |
|------------------------------|---------|
| `internal/dynamic`           | done    |
| Config loader                | done    |
| Docker/Swarm collector       | done    |
| Label helpers                | done    |
| Dynamic configuration builder | done   |
| Compiler/merger              | done    |
| HTTP server                  | done    |
| Poller and entrypoint        | done    |
| Live config reload (inotify, SIGHUP) | done |
| Dockerfile, Makefile, compose example | done |

## How containers become routes

- Each container (or Swarm entry) decodes its `traefik.*` labels into its own configuration; the results are merged in name order.
- Without router labels, one router is created with the `docker.defaultRule` template (default ``Host(`{{ normalize .Name }}`)``), but only when the container defines at most one service. With no service labels, a default service is created.
- Server port: the `server.port` label, otherwise the lowest exposed port. A `server.url` label cannot be combined with a port or scheme.
- Swarm: services without `lbswarm` route to each running task; with `traefik.swarm.lbswarm=true` they route to the service VIP. Entries without a network address are skipped.
- Replicas of the same service (same name) merge into one load balancer with every replica as a server; identical routers are kept once. Conflicting definitions of the same router, service, etc. are removed and logged.

## Static vs discovered configuration

The compiler produces the configuration served to Traefik: the static `dynamic` section plus every discovered (container or Swarm) resource.

- Static entries always win. A discovered resource whose kind and name already exist in the static configuration is dropped and logged as a warning (`dynamic entry dropped: static configuration takes precedence`). Each dropped entry logs one warning per poll.
- Names are compared per kind, so a router `x` and a service `x` do not conflict.
- A discovered router may point at a static service of the same name when that service was dropped; this is intentional.
- TLS certificates are deduplicated by certificate and key file; static certificates come first.
- Kinds checked: `http` routers, services, middlewares and serversTransports; `tcp` routers, services, middlewares and serversTransports; `udp` routers and services; `tls` options and stores.
- A discovered HTTP router that references an unknown service or middleware is logged as a warning and kept. Names containing `@` (for example `api@internal`) are not checked.

## Docker access

Detour reads the Docker Engine API with read-only `GET` requests only. Point `docker.endpoint` at either:

- a local socket: `unix:///var/run/docker.sock` (mount it read-only into the Detour container), or
- a socket proxy: `tcp://socket-proxy:2375` (plain HTTP; with a `docker.tls` block it becomes HTTPS).

Environment variables such as `DOCKER_HOST` are ignored; the config file is the only source.

When using a socket proxy such as `tecnativa/docker-socket-proxy`, enable these permissions:

| Proxy variable | Needed for |
|----------------|------------|
| `CONTAINERS=1` | container list and inspect (and connected containers for `network_mode: container:`) |
| `SERVICES=1`   | Swarm service list |
| `TASKS=1`      | Swarm task list |
| `NETWORKS=1`   | network list (Swarm networks) |
| `NODES=1`      | node lookup for task node IPs |

`/_ping` and `/version` are required and are enabled by default in that proxy. Set `INFO=1` only if you need it; Detour does not call `/info`.
