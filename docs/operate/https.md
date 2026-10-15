# Serve over HTTPS

Jocasta speaks plain HTTP. Put a reverse proxy with a certificate in front of
it.

The session cookie is HTTPS-only by default, so plain HTTP sign-in from any
address but `localhost` needs `server.auth.cookie_secure: false`. Remove it
once the proxy is up.

## Caddy

[Caddy](https://caddyserver.com) fetches and renews the certificate. On the
same machine:

```
jocasta.example.com {
    reverse_proxy localhost:8080
}
```

For a name only your network resolves, add `tls internal`. Caddy then signs
the certificate itself, and each browser has to trust it once.

## Other proxies

Pass the `Host` header through unchanged. Jocasta checks it against `Origin`
to refuse cross-site form posts from browsers that send no `Sec-Fetch-Site`.
In nginx: `proxy_set_header Host $host;`.

## Name the proxy

Sign-in attempts are limited per client address. Behind a proxy every request
comes from the proxy's address, so all clients share one allowance until
Jocasta knows to read the client from `X-Forwarded-For`:

```yaml
server:
  trusted_proxies:
    - "127.0.0.1"
```

List each proxy's address or range. Caddy, nginx and Traefik set the header.

## Close the plain port

With the proxy on the same machine, listen on loopback only:

```yaml
server:
  host: "127.0.0.1"
```

The container image sets `JOCASTA_SERVER__HOST=0.0.0.0`, which wins over the
file. Pass `-e JOCASTA_SERVER__HOST=127.0.0.1` to `docker run` instead.
