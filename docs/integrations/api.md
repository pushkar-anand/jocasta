# JSON API

Jocasta serves its inventory as JSON under `/api`, for scripts and
dashboards. The API uses the same API tokens as the [MCP server](mcp.md).

## Authenticate

Create an [API token](../operate/users.md#api-tokens) and send it with each
request:

```sh
curl -H "Authorization: Bearer <token>" http://<host>:8080/api/devices
```

A `read` token can make only `GET` requests. A `read_write` token can also
change devices. A token never expires, unless you selected 30 days, 90 days
or 1 year when you created it. Jocasta refuses an expired token, as it
refuses a revoked token. The expired token stays in the list until you revoke
it.

For health checks, `GET /api/livez` answers without a token.

## Endpoints

| Request | Returns |
|---|---|
| `GET /api/livez` | Whether the server is up. |
| `GET /api/stats` | Counts across the inventory. |
| `GET /api/groups` | The device groups in use. |
| `GET /api/devices` | The device list. |
| `GET /api/devices/{id}` | One device. |
| `PATCH /api/devices/{id}` | Changes a device's label, group, type, notes or ignored flag. Needs a `read_write` token. |
| `PATCH /api/devices/{id}/watch` | Starts or stops watching a device. Needs a `read_write` token. |
| `GET /api/devices/{id}/events` | A device's change log. |
| `GET /api/devices/{id}/traffic` | Who a device talked to. Needs [traffic recorded](../plugins/netflow.md). |
| `GET /api/devices/{id}/dns` | What a device looked up. Needs [lookups recorded](../plugins/dns.md). |
| `GET /api/events` | The change log across all devices. |
| `GET /api/scans` | Past scans. |

Jocasta returns an error as an
[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem document. Its
`title` and `detail` tell you what to change.

## Browser access

A page on another origin can read `/api` only if
`server.cors.allowed_origins` lists its origin. See the
[configuration reference](../reference/configuration.md#server).
