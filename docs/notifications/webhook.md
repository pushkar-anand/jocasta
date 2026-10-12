# Check a webhook's signature

A webhook request is a POST whose JSON body has `title`, `message`,
`scan_id` and `events`. Two headers come with it:

- `X-Jocasta-Signature-256`: `sha256=` and the hex HMAC-SHA256 of the raw
  body, keyed with the destination's `secret`.
- `X-Jocasta-Delivery`: a random id for this request. Log it, and drop a
  delivery you have already handled.

To check a request, compute the HMAC over the body exactly as received, before
parsing it, and compare it with the header in constant time. In Go:

```go
mac := hmac.New(sha256.New, []byte(secret))
mac.Write(body)
want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
ok := hmac.Equal([]byte(want), []byte(r.Header.Get("X-Jocasta-Signature-256")))
```

Reject the request when `ok` is false.

