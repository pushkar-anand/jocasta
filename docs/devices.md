# Label, group and ignore devices

These fields are yours. Scans never write them, and they stay with the device
when its address or name changes. Editing needs an Admin or Editor account.

- **Label**: the name shown everywhere. Up to 200 characters.
- **Group**: files devices together, such as `Office`. The device list filters
  by it. Up to 100 characters.
- **Type**: sets the icon. **Auto** keeps Jocasta's guess; see
  [How a device gets its type](classification.md).
- **Notes**: free text. Up to 2,000 characters.
- **Ignored**: hides the device from lists and counts.
- **Watched**: logs when it goes quiet or comes back; see
  [Get told when a device goes quiet](notifications/watch.md).

## Edit a device

1. In **Devices**, select the device.
2. Select **Edit details**, change the fields, and select **Save**.

An empty field is cleared. Each edit goes in the change log.

To change only the label and group, select the pencil at the end of the row in
the device list.

## Ignore a device

For a device you know and never want to see, such as a neighbour's printer:
in **Edit details**, tick **Ignore this device** and select **Save**.

The sweep still records it. The port scan skips it, and notifications leave it
out. **Show ignored** in the device list filters brings it back into view.

## From a script or an agent

`PATCH /api/devices/{id}` in the [JSON API](integrations/api.md), or the
`update_device_curation` tool in the [MCP server](integrations/mcp.md). Both
need a `read_write` token. The MCP prompt `triage_devices` proposes a label,
group and type for each device that has none.
