# Notifications

Jocasta can send the changes from each scan to these destinations:

- an [ntfy](<https://ntfy.sh>) topic
- your own URL, as a signed JSON POST
- any service that accepts an HTTP request, such as Discord, Slack or Telegram. See [Send to another service](<https://pushkar-anand.github.io/jocasta/0.14/notifications/other-services/index.md>).
- Under `notify` in the config file, add each destination by name. Give each destination one service block:

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

A destination is on, unless it has `enabled: false`. 2. Restart Jocasta, so that it reads the change. 3. Open the account menu at the top right, and select **Notifications**. 4. For each destination, tick the kinds of change to send to it. 5. Select **Save**. 6. Optional: select **Send test**. The destination gets a message with the title "Jocasta test".

Jocasta sends one message for each scan that changes something, such as "2 new devices on 192.0.2.0/24". In the first scan of a network, every device is new. So Jocasta sends only a count for that scan ("Found 42 devices"). Jocasta does not send changes to devices that you ignore, or your own edits. If Jocasta cannot deliver a message, it logs the failure and shows it on the Notifications page. It does not send the message again.

The [configuration reference](<https://pushkar-anand.github.io/jocasta/0.14/reference/configuration/#notify>) lists every `notify` setting, with its default and allowed values.
