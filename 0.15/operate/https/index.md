# Serve over HTTPS

Jocasta serves only plain HTTP. Put a reverse proxy with a certificate in front of it.

By default, the session cookie works only over HTTPS. So to sign in over plain HTTP from an address other than `localhost`, set `server.auth.cookie_secure: false`. When the proxy works, remove this setting.

## Caddy

[Caddy](<https://caddyserver.com>) gets and renews the certificate. On the same machine:

```
jocasta.example.com {
    reverse_proxy localhost:8080
}
```

If only your network resolves the name, add `tls internal`. Caddy then signs the certificate itself. Each browser must trust it once.

## Other proxies

Pass the `Host` header through without a change. Jocasta compares it with `Origin`. This lets Jocasta refuse cross-site form posts from browsers that do not send `Sec-Fetch-Site`. In nginx, use `proxy_set_header Host $host;`.

## Name the proxy

Jocasta limits sign-in attempts for each client address. Behind a proxy, every request comes from the address of the proxy. So all clients share one limit. To prevent this, tell Jocasta to read the client address from `X-Forwarded-For`:

```
server:
  trusted_proxies:
    - "127.0.0.1"
```

List the address or range of each proxy. Caddy, nginx and Traefik set the header.

## Close the plain port

If the proxy is on the same machine, listen only on loopback:

```
server:
  host: "127.0.0.1"
```

The container image sets `JOCASTA_SERVER__HOST=0.0.0.0`, and this value wins over the file. So in a container, pass `-e JOCASTA_SERVER__HOST=127.0.0.1` to `docker run` instead.
