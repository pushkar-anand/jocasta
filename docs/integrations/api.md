# JSON API

Jocasta serves its inventory as JSON under `/api`, for scripts and
dashboards. It takes the same API tokens as the [MCP server](mcp.md).

## Authenticate

Create an API token from the account menu's **API tokens** page (see
[Create an API token](../operate/users.md#create-an-api-token)), and send it
with each request:

```sh
curl -H "Authorization: Bearer <token>" http://<host>:8080/api/devices
```

A `read` token can make `GET` requests only. A `read_write` token can also
change devices. A token never expires unless you chose 30 days, 90 days or 1
year when you created it. An expired token is refused like a revoked one, and
stays listed until you revoke it.

`GET /api/livez` answers without a token, for health checks.

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

An error comes back as an [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457)
problem document, with a `title` and a `detail` saying what to change.

## Browser access

A page on another origin can read `/api` only when its origin is listed in
`server.cors.allowed_origins`. See the
[configuration reference](../reference/configuration.md#server).
