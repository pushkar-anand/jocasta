# Notifications

Jocasta can send what each scan changed to an [ntfy](<https://ntfy.sh>) topic, as a signed JSON POST to a URL of your own, or to any service that takes an HTTP request, such as Discord, Slack or Telegram. See [Send to another service](<https://pushkar-anand.github.io/jocasta/0.11/notifications/other-services/index.md>).

1. Under `notify` in the config file, add each destination by name, with one service block, then restart Jocasta:

```
notify:
  phone:
    ntfy:
      url: "https://ntfy.example.com/jocasta"   # the topic's address, as ntfy shows it
      token: ""                                 # or JOCASTA_NOTIFY__PHONE__NTFY__TOKEN
  automation:
    webhook:
      url: "https://hooks.example.com/jocasta"
      secret: "change-me"                       # required; signs each request
```

A destination is on unless it says `enabled: false`. 2. In **Settings → Notifications**, tick the kinds of change each destination is sent, then select **Save**. 3. Optional: select **Send test**. A message titled "Jocasta test" arrives at the destination.

Each scan that changes something is sent as one message, such as "2 new devices on 192.0.2.0/24". The first scan of a network, when every device it finds is new, is sent as a count ("Found 42 devices"). Changes to devices you ignore, and your own edits, are left out. A message that cannot be delivered is logged and shown on the settings page, and is not sent again.

Every `notify` setting, with its default and allowed values, is in the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.11/reference/configuration/#notify>).
