<h1 align="center">Fluxgate Core</h1>

<h3 align="center">A compatibility-first, independently maintained proxy core.</h3>

<p align="center">
  <a href="https://goreportcard.com/report/github.com/JunZ-Leo/fluxgate-core">
    <img src="https://goreportcard.com/badge/github.com/JunZ-Leo/fluxgate-core?style=flat-square">
  </a>
  <img src="https://img.shields.io/github/go-mod/go-version/JunZ-Leo/fluxgate-core/main?style=flat-square">
  <img src="https://img.shields.io/github/license/JunZ-Leo/fluxgate-core?style=flat-square">
</p>

Fluxgate Core is a gradual, compatibility-preserving rewrite of the
MetaCubeX proxy core. The project keeps existing Clash-compatible
configuration and controller behavior stable while replacing internal
subsystems behind explicit compatibility tests.

The Go module path remains `github.com/metacubex/mihomo` temporarily to avoid
mixing a repository-wide import migration with the initial compatibility
rewrite. It will move to the Fluxgate namespace in a dedicated change.

## Installation identity and coexistence

Fluxgate is installed independently of Mihomo; it does not replace, uninstall,
or create an alias for `mihomo`.

| Component | Fluxgate identity |
| --- | --- |
| Executable / package | `fluxgate` (`fluxgate.exe` on Windows) |
| Default user configuration | `~/.config/fluxgate/config.yaml` |
| Packaged executable | `/usr/bin/fluxgate` |
| Packaged configuration | `/etc/fluxgate/config.yaml` |
| systemd unit | `fluxgate.service` |
| systemd instance | `fluxgate@NAME.service`, using `/etc/fluxgate/NAME/config.yaml` |
| Container executable / configuration volume | `/fluxgate` / `/root/.config/fluxgate/` |

There is no automatic configuration migration. Existing command-line flags,
controller API fields, and `CLASH_` environment variables remain compatible.
Use `-d` or `-f` explicitly if you deliberately want to read an old directory or
configuration file; otherwise keep configurations and runtime data separate.
Some protocol identifiers intentionally retain their upstream names.

**Before running Fluxgate alongside Mihomo, configure distinct listener,
controller, and DNS ports, and avoid conflicting TUN devices or routing rules.**
The shipped sample still uses mixed port `7890`; separate names alone do not
prevent runtime conflicts. Packages do not automatically enable or start
services. Review `/etc/fluxgate/config.yaml` before starting `fluxgate.service`.
For an instance, create its configuration directory and file first; the
template does not copy the default configuration. The inherited systemd
capabilities are unchanged.

## Rewrite status

- [x] Configuration defaults and partial override compatibility baseline
- [x] Plain and age-encrypted configuration loading boundary
- [x] Configuration file source loading boundary
- [x] Rule payload parsing boundary
- [x] Simple rule construction boundary
- [x] DNS nameserver parsing usable without application configuration initialization
- [ ] DNS resolver behavior and lifecycle boundary
- [ ] Controller API boundary
- [ ] Inbound and outbound protocol boundaries
- [ ] TUN and platform integration boundaries

DNS transport cancellation closes the active UDP/TCP or DNS-over-TLS connection,
including a TCP retry after a truncated UDP reply. Canceled DoT exchanges are
not returned to the connection pool; successful exchanges remain reusable.
The existing per-exchange timeout is unchanged. This does not cancel shared
resolver queries or change the resolver's background cache-refresh policy.

## Features

- Local HTTP/HTTPS/SOCKS server with authentication support
- VMess, VLESS, Shadowsocks, Trojan, Snell, TUIC, Hysteria protocol support
- Built-in DNS server that aims to minimize DNS pollution attack impact, supports DoH/DoT upstream and fake IP.
- Rules based off domains, GEOIP, IPCIDR or Process to forward packets to different nodes
- Remote groups allow users to implement powerful rules. Supports automatic fallback, load balancing or auto select node
  based off latency
- Remote providers, allowing users to get node lists remotely instead of hard-coding in config
- Netfilter TCP redirecting for gateway deployments with `iptables`.
- Comprehensive HTTP RESTful API controller

## Dashboard

The controller API remains compatible with dashboards such as
[metacubexd](https://github.com/MetaCubeX/metacubexd).

## Configuration example

The configuration example is available at
[`docs/config.yaml`](https://github.com/JunZ-Leo/fluxgate-core/blob/main/docs/config.yaml).

## Docs

Until independent documentation is complete, the compatible configuration and
API surfaces are documented in the
[upstream documentation](https://wiki.metacubex.one/).

## For development

Requirements:
[Go 1.20 or newer](https://go.dev/dl/)

Build Fluxgate Core:

```shell
git clone https://github.com/JunZ-Leo/fluxgate-core.git
cd fluxgate-core
go mod download
go build -o fluxgate
```

On Windows, use `go build -o fluxgate.exe`. The explicit output name is required
while the Go module path still ends in `mihomo`.

Set go proxy if a connection to GitHub is not possible:

```shell
go env -w GOPROXY=https://goproxy.io,direct
```

Build with gvisor tun stack:

```shell
go build -tags with_gvisor -o fluxgate
```

Make targets also produce `bin/fluxgate-<os>-<arch>` (with `.exe` on Windows).
Branch builds, including `main`, use `dev-<commit>` by default (the legacy
`Alpha`/`Beta` branch labels retain their prerelease prefixes). A detached exact
tag or an explicit `VERSION=vMAJOR.MINOR.PATCH` pins the build version.
The Nix package/output is named `fluxgate`; its inherited Go 1.19 builder and
vendor hash have not been modernized or validated, so Nix is not yet a verified
installation path.

### Releases

There is no published Fluxgate release yet. The following describes the release
pipeline, not an already available download or a completed core rewrite.

The [Build workflow](https://github.com/JunZ-Leo/fluxgate-core/actions/workflows/build.yml)
publishes to this repository's
[GitHub Releases](https://github.com/JunZ-Leo/fluxgate-core/releases):

- Run **Build > Run workflow**, select `main` (or the intended release ref), and
  enter a version such as `v0.1.0` or `v0.1.0-rc.1`; or
- Push a `vMAJOR.MINOR.PATCH` tag, optionally with a SemVer prerelease suffix,
  pointing to the intended commit. Lightweight and annotated tags are supported.

The workflow validates the version and captures the event's commit before the
build matrix starts. Artifacts, the release tag, and publication use that same
immutable commit, even if the selected branch advances during the build. Manual
runs create a missing tag without force; an existing tag is accepted only when
it resolves to the built commit. No branch or existing tag is moved. The first
release needs no previous tag or upstream version lookup; release notes are
generated by GitHub. Prerelease versions are marked as GitHub prereleases.
Binary archives are named `fluxgate-<os>-<arch>-<version>.gz`, or `.zip` on
Windows. Their binary member is `fluxgate-<os>-<arch>` (with `.exe` on Windows);
rename the extracted executable to `fluxgate` or `fluxgate.exe` for a standalone
installation. Architecture/feature suffixes such as `amd64-v1` are retained.
DEB, RPM, and Pacman packages install the independent paths above and use the
package name `fluxgate`. Package versions omit the leading `v`. The Go module
path is unchanged.
Compatibility builds still use the upstream patched Go toolchains, and container
geodata still comes from the upstream datasets; this identity change does not
replace those dependencies.
After merging the build artifacts, the release workflow generates a sorted
`checksums.txt` with SHA-256 hashes of the release files, excluding the manifest
itself, before tagging and publication.

Automatic core upgrades use this repository's **stable releases only**, including
when no channel is supplied. Alpha-channel requests are rejected rather than
falling back to upstream builds; prereleases and PR artifacts are not automatic
upgrade sources. Until the first stable release exists, no stable update is
available. Updating Fluxgate does not update an installed Mihomo executable.
The updater resolves the stable version from `version.txt`, then fetches the
archive and checksum manifest from that version's release paths rather than
following `latest` again. Missing or mismatched checksums prevent replacement.

Pull requests only build artifacts: they never publish, and use the deterministic
version `v0.0.0-dev.<full-build-commit-SHA>` without a version API request.

Docker Hub publication is **disabled by default**. To opt in, set the repository
Actions variable `ENABLE_DOCKER_PUBLISH` to `true` and configure the
`DOCKER_HUB_USER` and `DOCKER_HUB_TOKEN` Actions secrets with permission to push
`docker.io/junz-leo/fluxgate-core` (or your fork's lowercased repository name).
Docker runs only after a successful GitHub release, uses its exact version tag
and commit, and updates `latest` only for stable versions. Missing Docker secrets
do not affect the default GitHub-only release path.

Run the focused, offline release and packaging contract tests with Go, Git,
Bash, Make, gzip, and the standard GNU `find`, `sort`, `xargs`, and `sha256sum`
utilities used on the Linux build runner:

```shell
go test ./.github/tests
bash -n .github/scripts/release.sh
sh -n docker/file-name.sh
make -n linux-amd64-v1.gz windows-amd64-v1.zip
```

These tests cover metadata, workflow wiring, tagging against local bare Git
repositories, fixture archives, packaging paths, systemd units, and exact
Docker artifact selection. They do not build the full platform matrix, install
packages or services, validate Nix, or perform a live release or Docker upload.

### IPTABLES configuration

Work on Linux OS which supported `iptables`

```yaml
# Enable the TPROXY listener
tproxy-port: 9898

iptables:
  enable: true # default is false
  inbound-interface: eth0 # detect the inbound interface, default is 'lo'
```

## Debugging

Check [wiki](https://wiki.metacubex.one/api/#debug) to get an instruction on using debug
API.

## Credits

- [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo), the upstream codebase
  and compatibility reference
- [Dreamacro/clash](https://github.com/Dreamacro/clash)
- [SagerNet/sing-box](https://github.com/SagerNet/sing-box)
- [riobard/go-shadowsocks2](https://github.com/riobard/go-shadowsocks2)
- [v2ray/v2ray-core](https://github.com/v2ray/v2ray-core)
- [WireGuard/wireguard-go](https://github.com/WireGuard/wireguard-go)
- [yaling888/clash-plus-pro](https://github.com/yaling888/clash)

## License

This software is released under the GPL-3.0 license. The repository preserves
the upstream Git history and attribution; see [`NOTICE`](NOTICE).

**In addition, any downstream projects not affiliated with `MetaCubeX` shall not contain the word `mihomo` in their names.**