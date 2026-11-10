# Detour

Detour is a lightweight Go HTTP server that acts as a Traefik HTTP provider. It merges static Traefik dynamic configuration read from YAML files with configuration derived from Docker and Docker Swarm labels, and serves the result to Traefik so routers, services, middlewares and TLS settings can be declared either way.

## Architecture

- Config loader: reads the Detour configuration file, including the static dynamic-configuration section.
- Docker/Swarm collector: watches containers and services and decodes their `traefik.*` labels.
- Compiler/merger: turns label-derived configuration into Traefik objects and merges it with the static configuration.
- HTTP server: serves the merged configuration to Traefik's HTTP provider.

## Configuration

Detour reads one YAML file, passed at startup. The file is read once; there is no hot reload, so restart Detour after changing it. Unknown keys are an error. See [`config.example.yaml`](config.example.yaml) for a fully commented example with every option.

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
| `mode`              | `docker`                      | Label sources: `docker`, `swarm`, `both`, `off` (empty means `docker`). |
| `containers`        | from `mode`                   | Explicit boolean; overrides whether container labels are collected. |
| `swarm`             | from `mode`                   | Explicit boolean; overrides whether Swarm service labels are collected. |
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
| `defaultRule`       | ``Host(`{{ normalize .Name }}`)`` | Router rule generated when a container sets none. |
| `constraints`       |                               | Only containers matching this Traefik constraint expression are used. |
| `useBindPortIP`     | `false`                       | Use the IP from the published port binding instead of the container IP. |
| `allowEmptyServices`| `false`                       | Keep services with no endpoints instead of dropping them. |

#### Docker/Swarm mode rules

`mode` sets the baseline for each source:

| `mode`   | Containers | Swarm |
|----------|------------|-------|
| `docker` (or empty) | on  | off |
| `swarm`  | off        | on    |
| `both`   | on         | on    |
| `off`    | off        | off   |

An explicit `containers` or `swarm` boolean overrides the baseline for that source only. Contradictory combinations are allowed and the boolean wins: for example, `mode: swarm` with `containers: true` collects both containers and Swarm services, and `mode: both` with `swarm: false` collects only containers. Since the boolean always wins, the baseline is only relevant when a boolean is not set.

The global `server.updateInterval` drives polling for both containers and Swarm services.

### `dynamic`

Optional. Static Traefik dynamic configuration (`http`, `tcp`, `udp`, `tls` sections) in Traefik's own format. Use it for objects that are not backed by a container, such as a service pointing at an external host. Entries here win over Docker/Swarm entries with the same name.

## Status

| Component                    | Status  |
|------------------------------|---------|
| `internal/dynamic`           | done    |
| Config loader                | done    |
| Docker/Swarm collector       | planned |
| Compiler/merger              | planned |
| HTTP server                  | planned |
