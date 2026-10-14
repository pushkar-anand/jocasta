# Send to another service

The `http` service posts each message with a body you write as a template, so it reaches any service that takes an HTTP request. Copy the recipe for yours below, under `notify`.

The template is a [Go template](<https://pkg.go.dev/text/template>). It is given:

- `.Title` and `.Body`: the message's title, such as "2 new devices on 192.0.2.0/24", and its text, one change per line.
- `.ScanID`: the scan the changes came from, 0 for a test.
- `.Events`: each change, with `.Kind`, `.DeviceID`, `.Device` and `.Change`.
- `json`: writes a value as JSON, quoted and escaped. Write every value in a JSON body with it, as `{{ json .Title }}`: device names can hold quotes and the body holds line breaks.
- `truncate`: cuts a string to a number of characters and ends it with `…`, as `{{ .Body | truncate 2000 | json }}`. A message lists up to 20 changes, and long device names can take it past a service's limit, which the service answers with an error.

The body is sent as `application/json` unless `headers` names another `Content-Type`. Jocasta renders the template with two sample messages when it starts: one with a change, and the test message, which has none. If the template does not parse, or its output does not parse as JSON, Jocasta reports it and does not start.

A device names itself, so its name can hold text a chat service reads as a mention, such as `@everyone`, or as formatting. The Slack, Telegram, Gotify, Pushover and Apprise recipes send plain text. Discord has no plain-text mode, so its recipe turns mentions off and a device name can still show formatted.

Discord, with the channel's webhook URL from its Integrations settings:

```
discord:
  http:
    url: "https://discord.com/api/webhooks/<id>/<token>"
    body: '{"content": {{ printf "%s\n%s" .Title .Body | truncate 2000 | json }}, "allowed_mentions": {"parse": []}}'
```

Slack, with an incoming webhook URL. The message goes in a `plain_text` block, which Slack shows as written. The title is also sent as `text`, which Slack shows in its notifications:

```
slack:
  http:
    url: "https://hooks.slack.com/services/<path>"
    body: '{"text": {{ json .Title }}, "blocks": [{"type": "section", "text": {"type": "plain_text", "text": {{ printf "%s\n%s" .Title .Body | truncate 3000 | json }}}}]}'
```

Telegram, with a bot's token and the chat to post in:

```
telegram:
  http:
    url: "https://api.telegram.org/bot<token>/sendMessage"
    body: '{"chat_id": "<chat id>", "text": {{ printf "%s\n%s" .Title .Body | truncate 4096 | json }}}'
```

[Gotify](<https://gotify.net>), with an application's token:

```
gotify:
  http:
    url: "https://gotify.example.com/message"
    headers:
      X-Gotify-Key: "<app token>"
    body: '{"title": {{ json .Title }}, "message": {{ json .Body }}, "priority": 5}'
```

[Pushover](<https://pushover.net>), with an application's token and your user key:

```
pushover:
  http:
    url: "https://api.pushover.net/1/messages.json"
    body: '{"token": "<app token>", "user": "<user key>", "title": {{ json .Title }}, "message": {{ .Body | truncate 1024 | json }}}'
```

For email, SMS, Microsoft Teams, Signal and [many more](<https://github.com/caronc/apprise/wiki>), run an [Apprise API](<https://github.com/caronc/apprise-api>) server beside Jocasta. Save the services' Apprise URLs under a key in Apprise, then post to that key:

```
apprise:
  http:
    url: "http://apprise.example.com:8000/notify/jocasta"
    body: '{"title": {{ json .Title }}, "body": {{ json .Body }}}'
```

Home Assistant, n8n and Node-RED take any JSON, so they use the `webhook` service. For Home Assistant, add an automation with a webhook trigger and point a webhook destination at it. Home Assistant does not check the signature, but a secret is still required. The automation reads the message as `trigger.json.title` and `trigger.json.message`, and each change under `trigger.json.events`.

```
home_assistant:
  webhook:
    url: "http://homeassistant.local:8123/api/webhook/<webhook id>"
    secret: "change-me"
```
