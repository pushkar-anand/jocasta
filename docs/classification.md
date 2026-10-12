# How a device gets its type

After each scan, Jocasta guesses each device's type from:

- the vendor of its hardware address
- its name
- its open TCP ports
- the DNS-SD services it advertises, and a Google Cast device's model
- whether it made up its hardware address
- the name of its network, such as `IoT`
- whether it holds the `.1` of its subnet

## Rules

Each rule names a type and the facts it needs, such as vendor Brother for a
printer, or port 8123 for a Home Assistant hub. The matching rule with the
most facts wins. On a tie, the rule listed first wins.

The device page shows the guess as **auto** with a confidence. High: the rule
needed two or more facts, or two rules agree. Low: one weak fact, such as the
network's name, and nothing else. Medium: anything between.

When a later scan changes the guess, the change log records **Type changed**.

## Set the type yourself

Open the device, select **Edit details**, pick a type, and select **Save**.
Scans never change it. The device page still shows the guess. **Auto** goes
back to it.

## Ask why

The [MCP server](integrations/mcp.md)'s `explain_device` tool returns the
facts, the winning rule, and every other rule that matched.

## Types

Router, switch, access point, firewall, server, NAS, hypervisor, desktop,
laptop, phone, tablet, printer, camera, TV, media player, speaker, voice
assistant, game console, smart-home hub, smart-home device, wearable, VoIP
phone.
