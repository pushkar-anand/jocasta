# JSON API

Jocasta serves its inventory as JSON under `/api`, for scripts and dashboards. It takes the same API tokens as the [MCP server](<https://pushkar-anand.github.io/jocasta/0.11/integrations/mcp/index.md>).

## Authenticate

Create an API token under Settings, API tokens, and send it with each request:

```
curl -H "Authorization: Bearer <token>" http://<host>:8080/api/devices
```

A `read` token can make `GET` requests only. A `read_write` token can also change devices. A token can expire after 30 days, 90 days or 1 year. After that it is refused like a revoked one and stays listed until you revoke it.

`GET /api/livez` answers without a token, for health checks.

## Endpoints

| Request | Returns |
| --- | --- |
| `GET /api/livez` | Whether the server is up. |
| `GET /api/stats` | Counts across the inventory. |
| `GET /api/groups` | The device groups in use. |
| `GET /api/devices` | The device list. |
| `GET /api/devices/{id}` | One device. |
| `PATCH /api/devices/{id}` | Changes a device's label, group, type, notes or ignored flag. Needs a `read_write` token. |
| `PATCH /api/devices/{id}/watch` | Starts or stops watching a device. Needs a `read_write` token. |
| `GET /api/devices/{id}/events` | A device's change log. |
| `GET /api/devices/{id}/traffic` | Who a device talked to. Needs [traffic recorded](<https://pushkar-anand.github.io/jocasta/0.11/plugins/netflow/index.md>). |
| `GET /api/devices/{id}/dns` | What a device looked up. Needs [lookups recorded](<https://pushkar-anand.github.io/jocasta/0.11/plugins/#dns-resolvers>). |
| `GET /api/events` | The change log across all devices. |
| `GET /api/scans` | Past scans. |

An error comes back as an [RFC 9457](<https://www.rfc-editor.org/rfc/rfc9457>) problem document, with a `title` and a `detail` saying what to change.

## Browser access

A page on another origin can read `/api` only when its origin is listed in `server.cors.allowed_origins`. See the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.11/reference/configuration/#server>).
