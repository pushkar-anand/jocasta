# Serve over HTTPS

Jocasta serves plain HTTP. To reach it over HTTPS, put a reverse proxy that
holds a certificate in front of it. The session cookie is sent over HTTPS
only by default. If you set `server.auth.cookie_secure: false` to sign in over
plain HTTP, remove it once the proxy is in place.

## Use Caddy

[Caddy](https://caddyserver.com) gets and renews a certificate by itself. On
the same machine as Jocasta, this `Caddyfile` serves it at
`https://jocasta.example.com`:

```
jocasta.example.com {
    reverse_proxy localhost:8080
}
```

For a name only your own network resolves, Caddy cannot get a public
certificate. Add `tls internal` inside the block, and Caddy signs one with its
own authority, which each browser has to trust once.

## Use another proxy

Any reverse proxy works. Pass the request's `Host` header through unchanged.
To refuse form posts from other sites, Jocasta compares it with the `Origin`
header of browsers that send no `Sec-Fetch-Site` header. Caddy passes `Host`
by default, and nginx needs `proxy_set_header Host $host;`.

## Keep the plain port private

Once the proxy is in place, only the proxy needs to reach port 8080. With the
proxy on the same machine, set Jocasta to listen on the loopback address
alone:

```yaml
server:
  host: "127.0.0.1"
```

The container image sets `JOCASTA_SERVER__HOST=0.0.0.0`, which overrides the
file. With the container, pass `-e JOCASTA_SERVER__HOST=127.0.0.1` to
`docker run` instead.
