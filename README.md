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

## Rewrite status

- [x] Configuration defaults and partial override compatibility baseline
- [x] Plain and age-encrypted configuration loading boundary
- [x] Configuration file source loading boundary
- [x] Rule payload parsing boundary
- [x] Simple rule construction boundary
- [ ] DNS parsing and resolver boundary
- [ ] Controller API boundary
- [ ] Inbound and outbound protocol boundaries
- [ ] TUN and platform integration boundaries

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
go build
```

Set go proxy if a connection to GitHub is not possible:

```shell
go env -w GOPROXY=https://goproxy.io,direct
```

Build with gvisor tun stack:

```shell
go build -tags with_gvisor
```

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