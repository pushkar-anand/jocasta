# Send to another service

The `http` service posts each message with a body that you write as a
template. So it can reach any service that accepts an HTTP request. Find the
recipe for your service below, and copy it under `notify`.

The body is a [Go template](https://pkg.go.dev/text/template). The template
gets these values and functions:

- `.Title` and `.Body`: the title of the message, such as "2 new devices on
  192.0.2.0/24", and its text, with one change on each line.
- `.ScanID`: the scan that the changes came from. It is 0 for a test.
- `.Events`: each change, with `.Kind`, `.DeviceID`, `.Device` and `.Change`.
- `json`: writes a value as JSON, with quotes and escapes. Use it for every
  value in a JSON body, as `{{ json .Title }}`. Device names can contain
  quotes, and the body contains line breaks.
- `truncate`: cuts a string to a number of characters and adds `…` at the
  end, as `{{ .Body | truncate 2000 | json }}`. A message lists up to 20
  changes. Long device names can make it longer than the limit of a service.
  The service then returns an error.

Jocasta sends the body as `application/json`, unless `headers` sets a
different `Content-Type`. When Jocasta starts, it renders the template with
two sample messages. One sample has a change. The other is the test message,
which has no change. If the template does not parse, or its output is not
valid JSON, Jocasta reports the problem and does not start.

A device gives its own name. So its name can contain text that a chat
service reads as a mention, such as `@everyone`, or as formatting. The
Slack, Telegram, Gotify, Pushover and Apprise recipes send plain text.
Discord has no plain-text mode. So its recipe turns mentions off, but a
device name can still show with formatting.

Discord, with the webhook URL from the Integrations settings of the
channel:

```yaml
discord:
  http:
    url: "https://discord.com/api/webhooks/<id>/<token>"
    body: '{"content": {{ printf "%s\n%s" .Title .Body | truncate 2000 | json }}, "allowed_mentions": {"parse": []}}'
```

Slack, with an incoming webhook URL. The message goes in a `plain_text`
block, which Slack shows as it is. The recipe also sends the title as
`text`, which Slack shows in its notifications:

```yaml
slack:
  http:
    url: "https://hooks.slack.com/services/<path>"
    body: '{"text": {{ json .Title }}, "blocks": [{"type": "section", "text": {"type": "plain_text", "text": {{ printf "%s\n%s" .Title .Body | truncate 3000 | json }}}}]}'
```

Telegram, with the token of a bot and the chat to post in:

```yaml
telegram:
  http:
    url: "https://api.telegram.org/bot<token>/sendMessage"
    body: '{"chat_id": "<chat id>", "text": {{ printf "%s\n%s" .Title .Body | truncate 4096 | json }}}'
```

[Gotify](https://gotify.net), with the token of an application:

```yaml
gotify:
  http:
    url: "https://gotify.example.com/message"
    headers:
      X-Gotify-Key: "<app token>"
    body: '{"title": {{ json .Title }}, "message": {{ json .Body }}, "priority": 5}'
```

[Pushover](https://pushover.net), with the token of an application and your
user key:

```yaml
pushover:
  http:
    url: "https://api.pushover.net/1/messages.json"
    body: '{"token": "<app token>", "user": "<user key>", "title": {{ json .Title }}, "message": {{ .Body | truncate 1024 | json }}}'
```

For email, SMS, Microsoft Teams, Signal and
[many more](https://github.com/caronc/apprise/wiki), run an
[Apprise API](https://github.com/caronc/apprise-api) server next to Jocasta.
In Apprise, save the Apprise URLs of the services under a key. Then post to
that key:

```yaml
apprise:
  http:
    url: "http://apprise.example.com:8000/notify/jocasta"
    body: '{"title": {{ json .Title }}, "body": {{ json .Body }}}'
```

Home Assistant, n8n and Node-RED accept any JSON, so they use the `webhook`
service. For Home Assistant, add an automation with a webhook trigger. Then
set the `url` of a webhook destination to the webhook of that automation. Home Assistant does not check the
signature, but you must still set a secret. The automation reads the message
as `trigger.json.title` and `trigger.json.message`. It reads each change
under `trigger.json.events`.

```yaml
home_assistant:
  webhook:
    url: "http://homeassistant.local:8123/api/webhook/<webhook id>"
    secret: "change-me"
```

