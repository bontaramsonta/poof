# Static IP

Some APIs allowlist callers by source IP: a broker that binds API keys to a fixed address, a partner API with an IP allowlist, a database firewall. A normal poof Exit gets a fresh IP every Session, so it can't reach them. A **Static IP** is a named address that survives across Sessions; each new Exit borrows it.

The Exit itself is unchanged: fresh keys, the same dead-man's switch, gone on Ctrl-C. Only the address is kept. Design: [ADR-0006](./adr/0006-named-static-ip.md).

## Quick start

```
poof up india --static-ip groww --only api.groww.in
```

The first run allocates the address and prints it:

```
✓ static IP groww: <address> (kept after disconnect)
```

Register that address with the API provider. Every later `poof up india --static-ip groww` egresses from the same address.

## Commands

| Command | Does |
|---|---|
| `poof up <country> --static-ip <name>` | Launch an Exit that egresses from `<name>`, allocating it on first use |
| `poof up ... --only <host,...>` | Send only these hosts (and subdomains) through the Exit; everything else on the proxy goes direct |
| `poof static-ip` | List Static IPs in every region, and which Exit holds each |
| `poof static-ip release <country> <name>` | Give the address back to AWS. It is gone for good |

Names are lowercase letters, digits and dashes. A name is scoped to its country: `groww` in India and `groww` in Singapore are two addresses.

## How it behaves

- **One holder at a time.** A second `poof up` with the same name refuses while another Session's Exit is live. poof never takes the address from a live Exit.
- **Immediate reconnect works.** After Ctrl-C, AWS keeps the address on the terminating Exit for a minute or more. The next launch detaches it from any holder that is no longer pending or running.
- **Crashes are covered.** If the client dies, the dead-man's switch terminates the Exit within ~5 minutes and AWS detaches the address. Nothing is lost.
- **Nothing releases it implicitly.** Only `poof static-ip release` does, and it refuses while a live Exit holds it.

## Cost

An Elastic IP is billed at the AWS public-IPv4 rate whether or not an Exit holds it: about $0.005/hour, ~$3.60/month. Release names you no longer need.

## AWS permissions

On top of what plain `poof up` needs:

```
ec2:AllocateAddress
ec2:AssociateAddress
ec2:DisassociateAddress
ec2:DescribeAddresses
ec2:ReleaseAddress
```

Addresses are tagged `poof=1`, `poof:static-ip=<name>` and `Name=poof-static-<name>`, all set inside `AllocateAddress`.

## Pointing clients at it

poof serves SOCKS5 on `127.0.0.1:1080`. Clients MUST use `socks5h`, not `socks5`: with `socks5h` the hostname reaches poof, which `--only` needs to match and which keeps DNS in the tunnel.

**curl**

```
curl --proxy socks5h://127.0.0.1:1080 https://api.example.com/
```

**Node** (axios + [`socks-proxy-agent`](https://www.npmjs.com/package/socks-proxy-agent))

```js
import axios from 'axios';
import { SocksProxyAgent } from 'socks-proxy-agent';

const api = axios.create({
  baseURL: 'https://api.example.com',
  httpsAgent: new SocksProxyAgent('socks5h://127.0.0.1:1080', { timeout: 10_000 }),
  proxy: false, // don't also apply HTTP(S)_PROXY from the env
  timeout: 20_000,
});
```

**Python** (`pip install requests[socks]`)

```python
requests.get(url, proxies={"https": "socks5h://127.0.0.1:1080"})
```

**Yaak**: Settings → Proxy → Custom proxy configuration, with `socks5h://127.0.0.1:1080` for both HTTP and HTTPS. The setting is app-wide. Per command from the CLI instead: `HTTPS_PROXY=socks5h://127.0.0.1:1080 yaak request send <id>`.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `static IP <name> (...) is held by i-...` | Another Session is live. Disconnect it first |
| Requests hang, then time out | poof is suspended (Ctrl-Z), not stopped. Resume it with `fg`, or Ctrl-C and start again |
| Requests fail with connection refused | poof isn't running |
| The API rejects you though poof is up | The client isn't sending that host through the proxy, uses `socks5` instead of `socks5h`, or `--only` doesn't list the host |
| The address changed | It was released. A new `poof up` allocates a new one; update the allowlist |
