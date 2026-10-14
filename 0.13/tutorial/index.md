# Your first inventory

Run Jocasta in a container, sweep your network, name a device, and get a message on your phone when it goes quiet. About 10 minutes.

You need a Linux machine on the network with Docker, and the network's prefix. `ip -4 addr` shows it: `inet 192.0.2.20/24` means `192.0.2.0/24`.

## 1\. Write the config

Create `jocasta.yaml` in an empty directory:

```
networks:
  - "192.0.2.0/24"

server:
  auth:
    cookie_secure: false   # sign in over plain HTTP
```

## 2\. Start Jocasta

```
docker run -d --name jocasta --network host \
  -v jocasta-data:/data \
  -v ./jocasta.yaml:/data/jocasta.yaml:ro \
  ghcr.io/pushkar-anand/jocasta:latest
```

Host networking lets the sweep read hardware addresses.

## 3\. Create the admin account

Open `http://<host>:8080`. Enter a username and a password of 8 or more characters, and select **Create admin account**.

Until this account exists, anyone who reaches the page can create it.

## 4\. Look at the first sweep

The first sweep runs at startup. Open **Devices**: one row per device, with its addresses, vendor and name.

Rows with no vendor are usually phones and laptops, which make up their hardware address. Rows with no name belong to devices that did not answer a name lookup.

## 5\. Name a device

1. Select a device you recognise.
2. Select **Edit details**.
3. Set **Label** to `Office printer` and **Group** to `Office`.
4. Select **Save**.

The device list shows the new label.

## 6\. Watch it

On the device's page, select **Watch**.

A watched device goes quiet 15 minutes after the last scan that saw it. To try it, turn the device off for 20 minutes and open **Events**.

## 7\. Get a message on your phone

1. Install [ntfy](<https://ntfy.sh>) on your phone and subscribe to a topic. Topics are public, so pick a name nobody would guess, such as `jocasta-k3v9q2`.
2. Add it to `jocasta.yaml`:

   ```
   notify:
     phone:
       ntfy:
         url: "https://ntfy.sh/jocasta-k3v9q2"
   ```
3. Restart: `docker restart jocasta`.
4. Open the account menu, top right, and select **Notifications**.
5. Under `phone`, tick **Watched device went quiet** and **Watched device came back**. Select **Save**.
6. Select **Send test**. "Jocasta test" arrives on your phone.

## Next

- [How Jocasta works](<https://pushkar-anand.github.io/jocasta/0.13/concepts/index.md>)
- [Connect your router](<https://pushkar-anand.github.io/jocasta/0.13/plugins/index.md>) for devices on other VLANs
- [Serve over HTTPS](<https://pushkar-anand.github.io/jocasta/0.13/operate/https/index.md>)
