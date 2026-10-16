# Manage users and sign-in

## Roles

| Role | Can |
|---|---|
| Viewer | Read the inventory. Create read tokens. |
| Editor | Also edit devices. Create read/write tokens. |
| Admin | Also add users and set what each notification destination gets. |

The account that you create on the first run is the only admin.

## Add a user

1. Open the account menu, top right, and select **Users**.
2. Select **Add user**.
3. Enter a username (3 to 100 characters), a first password (8 or more), and
   a role.
4. Select **Create user**.
5. Give the password to the person.

The web UI cannot yet change a password or role, remove an account, or reset
a forgotten password.

## Sessions

A sign-in lasts up to 7 days, or 24 hours without use. If you do not tick
**Remember me**, it also ends when the browser closes. To change the limits,
set `server.auth.session_lifetime` and `server.auth.idle_timeout`.

After 10 wrong passwords, an account gets one try each minute.

## Two-factor authentication

1. Open the account menu and select **Security**.
2. Select **Turn on two-factor authentication**.
3. Scan the QR code with an authenticator app.
4. Enter the 6-digit code from the app.
5. Select **Turn on**.
6. Save the 10 recovery codes. Jocasta shows them only once. Each code signs
   you in once without the app.

On the same page, you can make new recovery codes or turn two-factor off.
Both actions ask for your password.

## API tokens

Scripts and agents use a token to reach the
[JSON API](../integrations/api.md) and the [MCP server](../integrations/mcp.md).

1. Open the account menu and select **API tokens**.
2. Select **Create a token**.
3. Enter a name.
4. Select **Viewer** (`read`) or **Editor** (`read_write`).
5. Select an expiry. By default, a token never expires.
6. Select **Create token**.
7. Copy the token. Jocasta shows it only once.

A token cannot do more than its account. When you revoke a token, it stops
working immediately.
