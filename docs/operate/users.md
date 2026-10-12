# Manage users and sign-in

Everyone who opens the web UI signs in with an account. Scripts and agents use
API tokens instead.

## Roles

| Role | Can |
|---|---|
| Viewer | Read the inventory. Create read tokens. |
| Editor | Also edit devices: label, group, type, notes, ignore and watch. Create read and read/write tokens. |
| Admin | Also add users and choose which changes each notification destination is sent. |

An instance has one admin, the account created on first run. Every account
added later is a Viewer or an Editor.

## Add a user

1. Open the account menu at the top right, and select **Users**.
2. Select **Add user**.
3. Enter a username of 3 to 100 characters and a first password of at least 8.
   Choose **Viewer** or **Editor**.
4. Select **Create user**, then share the password with the person.

The web UI has no page yet to change a password or a role, or to remove an
account, and no way to reset a forgotten password. Keep the admin password in
a password manager.

## Sign-in sessions

With **Remember me** ticked, a sign-in lasts until it reaches
`server.auth.session_lifetime` (7 days by default) or goes unused for
`server.auth.idle_timeout` (24 hours by default). Without it, the sign-in also
ends when you close the browser.

After 10 wrong passwords, an account is allowed one more try a minute.

The session cookie is sent over HTTPS only. To sign in over plain HTTP, set
`server.auth.cookie_secure: false`, or better, [serve Jocasta over
HTTPS](https.md).

## Turn on two-factor authentication

1. Open the account menu, and select **Security**.
2. Select **Turn on two-factor authentication**.
3. Scan the QR code with an authenticator app, or enter the key shown under it.
4. Enter the 6-digit code the app shows, then select **Turn on**.
5. Save the 10 recovery codes the page now shows: select **Copy all** or
   **Download as text**. They are not shown again. Each code signs you in
   once in place of an app code.

From then on, signing in asks for a code after the password. On the same page,
**Regenerate recovery codes** replaces the codes, and **Turn off two-factor
authentication** goes back to the password alone. Both ask for your password.

## Create an API token

The [JSON API](../integrations/api.md) and the
[MCP server](../integrations/mcp.md) take a token in place of a sign-in.

1. Open the account menu, and select **API tokens**.
2. Select **Create a token**.
3. Name the token after what will use it, such as `Grafana dashboard`. Choose
   its permission: **Viewer** (`read`) reads the inventory, and **Editor**
   (`read_write`) also edits devices. Choose when it expires: never, or after
   30 days, 90 days or 1 year.
4. Select **Create token**, and copy the token. It is shown only once.

A token can do no more than the account that created it. Revoke a token from
the same page when nothing uses it any more. A script that still sends it is
refused straight away.
