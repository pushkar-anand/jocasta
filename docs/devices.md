# Label, group and ignore devices

Each device has fields that only you set. No scan or plugin writes them, so
they stay with the device when its address or name changes. Editing them needs
an Admin or Editor account; see [Manage users and sign-in](operate/users.md).

| Field | What it does |
|---|---|
| Label | The name shown for the device everywhere, in place of the name a source gave it. Up to 200 characters. |
| Group | A name to file devices under, such as `Office` or `Cameras`. The device list filters by it. Up to 100 characters. |
| Type | The kind of device, which sets its icon. Leave it on **Auto** to keep Jocasta's guess; see [How a device gets its type](classification.md). |
| Notes | Free text, such as where the device is or who owns it. Up to 2,000 characters. |
| Ignored | Hides the device from the device list, the network pages and the counts. |
| Watched | Logs when the device goes quiet or comes back; see [Get told when a device goes quiet](notifications/watch.md). |

## Edit a device

1. In **Devices**, select the device.
2. Select **Edit details**.
3. Change any field, then select **Save**.

A field left empty is cleared. The change log records each edit.

To change only the label and group, select the pencil at the end of the
device's row in the device list, and edit them in place.

## Ignore a device

Ignore a device you know and do not want to see, such as a neighbour's
printer that answers on a shared network. In its **Edit details**, tick
**Ignore this device**, then select **Save**.

The sweep still records an ignored device. The port scan skips it, and
notifications leave its changes out. To list ignored devices, tick **Show
ignored** in the device list filters.

## Edit from a script or an agent

The [JSON API](integrations/api.md) changes these fields with
`PATCH /api/devices/{id}`, and the [MCP server](integrations/mcp.md) with the
`update_device_curation` tool. Both need a `read_write` token. The MCP
server's `triage_devices` prompt proposes a label, group and type for each
device that has none.
