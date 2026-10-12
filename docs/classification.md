# How a device gets its type

Jocasta guesses what kind of device each record is, such as a phone, a printer
or a camera, and shows the guess as the device's icon and type. It guesses
again after every scan that touches the device, from what the inventory
already knows:

- the vendor its hardware address is registered to
- its name
- the TCP ports it answers on
- the services it advertises over DNS-SD, and for a Google Cast device, its
  model
- whether its hardware address is one it made up
- the name of the network it is on, such as `IoT` or `cameras`
- whether it holds the `.1` address of its subnet, where a home router usually
  answers

## How the guess is made

Each rule names a type and the facts it needs, such as "vendor Brother" for a
printer or "port 8123" for a Home Assistant hub. Of the rules whose facts all
hold, the one that needs the most facts wins. Among equally specific rules,
the one listed first wins, and the list runs from the most telling rules to
the weakest. The same facts always give the same type.

The device page shows a guess as **auto**, with how sure it is:

| Confidence | When |
|---|---|
| High | The winning rule needed two or more facts, or two or more rules point at the same type. |
| Medium | One rule decided, on one fact. |
| Low | One weak rule decided, and nothing backs it up. A network's name and a made-up hardware address are weak facts. |

A device no rule matches has no type.

When a later scan moves the guess from one type to another, the change log
records **Type changed**.

## Set the type yourself

Every rule is a heuristic a device can defeat. To fix a wrong guess, open the
device, select **Edit details**, pick a type, and select **Save**. Your type
replaces the guess, and scans never change it. The device page still shows
what Jocasta would have guessed.

To go back to the guess, set the type to **Auto**.

## See why a device got its type

The [MCP server](integrations/mcp.md)'s `explain_device` tool returns the facts
the classifier used, the rule that decided, and every other rule that matched.
Ask an agent "Why is this device a TV?" to see it.

## Types

The types are: router, switch, access point, firewall, server, NAS,
hypervisor, desktop, laptop, phone, tablet, printer, camera, TV, media player,
speaker, voice assistant, game console, smart-home hub, smart-home device,
wearable and VoIP phone.
