# Check a webhook's signature

A webhook request is a POST. Its JSON body has these fields:

- `title` and `message`: the text of the notification.
- `scan_id`: the scan the changes came from. It can be missing.
- `events`: the changes. Each change has a `kind`. It can also have
  `device_id`, `device` and `change`.

The request has two headers:

- `X-Jocasta-Signature-256`: `sha256=`, then the hex HMAC-SHA256 of the raw
  body. The key is the `secret` of the destination.
- `X-Jocasta-Delivery`: a random ID for this request. Log it. If you already
  handled a delivery with this ID, discard the request.

To check a request, compute the HMAC over the body exactly as you got it.
Do this before you parse the body. Then compare the result with the header in
constant time. In Go:

```go
mac := hmac.New(sha256.New, []byte(secret))
mac.Write(body)
want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
ok := hmac.Equal([]byte(want), []byte(r.Header.Get("X-Jocasta-Signature-256")))
```

Reject the request when `ok` is false.

