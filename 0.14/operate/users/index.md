# Manage users and sign-in

## Roles

| Role | Can |
| --- | --- |
| Viewer | Read the inventory. Create read tokens. |
| Editor | Also edit devices. Create read/write tokens. |
| Admin | Also add users and set what each notification destination gets. |

The account created on first run is the only admin.

## Add a user

1. Open the account menu, top right, and select **Users**.
2. Select **Add user**.
3. Enter a username (3 to 100 characters), a first password (8 or more), and a role.
4. Select **Create user**, and give the person the password.

The web UI cannot change a password or role, remove an account, or reset a forgotten password yet.

## Sessions

A sign-in lasts up to 7 days, or 24 hours without use. Without **Remember me**, it also ends when the browser closes. Set the limits with `server.auth.session_lifetime` and `server.auth.idle_timeout`.

After 10 wrong passwords, an account gets one try a minute.

## Two-factor authentication

1. Open the account menu and select **Security**.
2. Select **Turn on two-factor authentication**.
3. Scan the QR code with an authenticator app.
4. Enter its 6-digit code and select **Turn on**.
5. Save the 10 recovery codes. They are shown once. Each one signs you in once without the app.

The same page regenerates the recovery codes or turns two-factor off. Both ask for your password.

## API tokens

Scripts and agents use a token to reach the [JSON API](<https://pushkar-anand.github.io/jocasta/0.14/integrations/api/index.md>) and the [MCP server](<https://pushkar-anand.github.io/jocasta/0.14/integrations/mcp/index.md>).

1. Open the account menu and select **API tokens**.
2. Select **Create a token**.
3. Name it, and pick **Viewer** (`read`) or **Editor** (`read_write`) and an expiry. The default never expires.
4. Select **Create token** and copy it. It is shown once.

A token can do no more than its account. Revoking it takes effect at once.
