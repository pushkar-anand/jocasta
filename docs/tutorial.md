# Your first inventory

This tutorial takes about 10 minutes. You run Jocasta in a container, let it
sweep your network, give a device a name of your own, and get told when that
device goes quiet. It needs no router access. To install another way, see
[Install](install.md).

You need:

- A Linux machine on the network you want to inventory, with Docker
  installed.
- The network's prefix. On that machine, `ip -4 addr` lists each interface's
  address with its prefix length, such as `inet 192.0.2.20/24`. The network is
  then `192.0.2.0/24`.

## 1. Write the config file

In an empty directory, create `jocasta.yaml` with your network's prefix:

```yaml
networks:
  - "192.0.2.0/24"

server:
  auth:
    cookie_secure: false   # lets you sign in over plain HTTP; see Serve over HTTPS
```

## 2. Start Jocasta

From the same directory, run the container on the host network, so it can
read the hardware address of each device that answers:

```bash
docker run -d --name jocasta --network host \
  -v jocasta-data:/data \
  -v ./jocasta.yaml:/data/jocasta.yaml:ro \
  ghcr.io/pushkar-anand/jocasta:latest
```

Jocasta keeps its database in the `jocasta-data` volume, so it survives a new
container.

## 3. Create the admin account

Open `http://<host>:8080`, with the machine's address in place of `<host>`.
The page asks you to set up the admin account. Enter a username and a password
of at least 8 characters, then select **Create admin account**.

Do this straight away: until the admin account exists, anyone who opens the
page first can create it.

## 4. See what the first sweep found

The first sweep runs as Jocasta starts and takes a few seconds. The Overview
shows your network with how many devices were seen recently, and the latest
changes.

Open **Devices**. Each row is one device, with its addresses, vendor and name.
Some rows have no vendor: phones and laptops make up a hardware address for
each Wi-Fi network, and a made-up address names no vendor. Some rows have no
name, because the device gave none when asked. The sweep repeats every 5
minutes.

## 5. Name a device

1. In **Devices**, select a device you recognise, such as your printer or
   NAS.
2. On its page, select **Edit details**.
3. In **Label**, enter a name, such as `Office printer`. In **Group**, enter
   `Office`.
4. Select **Save**.

The page shows **Details saved.**, and the device list now shows your label.
Scans never change a label, and it stays with the device when its address
changes.

## 6. Watch the device

On the same page, select **Watch**. The page shows **Watching Office
printer.**

Once no scan has seen a watched device for 15 minutes, it goes quiet, and the
change log records it. When a scan sees it again, it comes back. To see this
happen, turn the device off for 20 minutes, then open **Events**.

## 7. Get told on your phone

1. Install the [ntfy](https://ntfy.sh) app on your phone and subscribe to a
   topic with a name nobody could guess, such as `jocasta-k3v9q2`. Anyone who
   knows the name can read the topic.
2. Add the topic to `jocasta.yaml`:

    ```yaml
    notify:
      phone:
        ntfy:
          url: "https://ntfy.sh/jocasta-k3v9q2"
    ```

3. Restart Jocasta so it reads the change:

    ```bash
    docker restart jocasta
    ```

4. Open the account menu at the top right, and select **Notifications**.
5. Under **phone**, tick **Watched device went quiet** and **Watched device
   came back**, then select **Save**.
6. Select **Send test**. A message titled "Jocasta test" arrives on your
   phone.

From now on, your phone hears when the watched device goes quiet or comes
back.

## Next steps

- [How Jocasta works](concepts.md) explains sweeps, sources and presence.
- [Connect your router](plugins/index.md) to identify devices on other VLANs
  and see what is plugged in where.
- Turn on port scanning with `scan.ports.enabled: true`. See
  [Configure](configuration.md#what-to-turn-on-next).
- [Serve over HTTPS](operate/https.md) before you reach Jocasta from outside
  your network.
