# How a device gets its type

After each scan, Jocasta guesses the type of each device from these facts:

- the vendor of its hardware address
- its name
- its open TCP ports
- the DNS-SD services it advertises, and the model of a Google Cast device
- whether it made up its hardware address
- the name of its network, such as `IoT`
- whether it has the `.1` address of its subnet

## Rules

Each rule names a type and the facts it needs. For example, vendor Brother means a printer, and port 8123 means a Home Assistant hub. Of the rules that match, the rule with the most facts wins. If two rules have the same number of facts, the rule listed first wins.

The device page shows the guess as **auto**, with a confidence:

- **High**: the rule needs two or more facts, or two rules agree.
- **Low**: the guess comes from one weak fact, such as the network's name, and nothing else.
- **Medium**: all other cases.

If a later scan changes the guess, the change log records **Type changed**.

## Set the type yourself

1. Open the device and select **Edit details**.
2. Select a type.
3. Select **Save**.

Scans never change a type that you set. The device page still shows the guess. To use the guess again, select **Auto**.

## Ask why

The `explain_device` tool of the [MCP server](<https://pushkar-anand.github.io/jocasta/0.15/integrations/mcp/index.md>) returns the facts, the rule that won, and every other rule that matched.

## Types

Router, switch, access point, firewall, server, NAS, hypervisor, desktop, laptop, phone, tablet, printer, camera, TV, media player, speaker, voice assistant, game console, smart-home hub, smart-home device, wearable, VoIP phone.
