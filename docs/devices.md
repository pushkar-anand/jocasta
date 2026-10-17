# Label, group and ignore devices

These fields are yours. Scans never change them. They stay with the device
when its address or name changes. To edit them, you need an Admin or Editor
account.

- **Label**: the name that Jocasta shows everywhere. Up to 200 characters.
- **Group**: puts devices together, such as `Office`. You can filter the
  device list by group. Up to 100 characters.
- **Type**: sets the icon. **Auto** keeps Jocasta's guess. See
  [How a device gets its type](classification.md).
- **Notes**: free text. Up to 2,000 characters.
- **Ignored**: hides the device from lists and counts.
- **Watched**: logs an event when the device goes quiet or comes back. See
  [Get told when a device goes quiet](notifications/watch.md).

## Edit a device

1. In **Devices**, select the device.
2. Select **Edit details**.
3. Change the fields.
4. Select **Save**.

If you leave a field empty, Jocasta clears it. The change log records each
edit.

To change only the label and group, select the pencil at the end of the row in
the device list.

## Ignore a device

Ignore a device that you know and never want to see, such as a neighbour's
printer:

1. In **Edit details**, tick **Ignore this device**.
2. Select **Save**.

The sweep still records the device. The port scan skips it, and
notifications leave it out. To see it again, select **Show ignored** in the
device list filters.

## From a script or an agent

Use `PATCH /api/devices/{id}` in the [JSON API](integrations/api.md), or the
`update_device_curation` tool in the [MCP server](integrations/mcp.md). Both
need a `read_write` token. The MCP prompt `triage_devices` suggests a label,
group and type for each device that has none.
