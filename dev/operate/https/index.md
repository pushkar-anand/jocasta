# Serve over HTTPS

Jocasta speaks plain HTTP. Put a reverse proxy with a certificate in front of it.

The session cookie is HTTPS-only by default, so plain HTTP sign-in from any address but `localhost` needs `server.auth.cookie_secure: false`. Remove it once the proxy is up.

## Caddy

[Caddy](<https://caddyserver.com>) fetches and renews the certificate. On the same machine:

```
jocasta.example.com {
    reverse_proxy localhost:8080
}
```

For a name only your network resolves, add `tls internal`. Caddy then signs the certificate itself, and each browser has to trust it once.

## Other proxies

Pass the `Host` header through unchanged. Jocasta checks it against `Origin` to refuse cross-site form posts from browsers that send no `Sec-Fetch-Site`. In nginx: `proxy_set_header Host $host;`.

## Close the plain port

With the proxy on the same machine, listen on loopback only:

```
server:
  host: "127.0.0.1"
```

The container image sets `JOCASTA_SERVER__HOST=0.0.0.0`, which wins over the file. Pass `-e JOCASTA_SERVER__HOST=127.0.0.1` to `docker run` instead.
