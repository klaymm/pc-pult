[Русский](README.md) | [English](README.en.md)

# pc-pult

![Release](https://img.shields.io/github/v/release/klaymm/pc-pult) ![License](https://img.shields.io/github/license/klaymm/pc-pult) ![Go](https://img.shields.io/github/go-mod/go-version/klaymm/pc-pult?filename=router/go.mod) ![Platform](https://img.shields.io/badge/platform-Keenetic%20%7C%20Windows-blue)

Control a Windows PC from a Telegram bot. The bot runs on a Keenetic or Netcraze router, so it answers even when the computer is off: the router wakes it over the network.

## Features

- **Power** - Wake-on-LAN, shutdown, restart, sleep, hibernate, lock, sign out, timer
- **Status** - CPU and NVIDIA GPU load and temperature, memory, disks, network, uptime
- **Screenshots** - all screens or one, the original is available as a file
- **Sound and music** - volume, pause, next and previous track
- **Messages** - a text window on the screen, text read aloud, text sent to the clipboard
- **Programs** - launch from a list, find a process and end it
- **Files** - browse folders, download, upload to the PC, delete
- **AnyDesk** - ID, service control and access password
- **Notifications** - Windows sign-in and sign-out, lock, overheating, low disk space
- **Users** - the owner invites people by link and picks their rights: guest, user or admin; actions are written to a log

## Installation

You need a Keenetic or Netcraze router with Entware (a package manager for routers), a PC with 64-bit Windows 10 or 11, and a bot created with @BotFather.

1. Connect to the router over SSH, type `exec sh` and run:

```
opkg update && opkg install ca-certificates wget-ssl && wget -qO- https://raw.githubusercontent.com/klaymm/pc-pult/main/install.sh | sh
```

2. The wizard asks for the bot token, gives you a link that makes you the owner, and prints a command for the PC.
3. Paste that command into PowerShell on the PC. It asks for administrator rights, installs the service and connects it to the router.

### If Telegram is blocked

The wizard offers three ways to reach Telegram: directly, through your own Cloudflare Worker (a free request forwarder, the wizard shows its code) or through a proxy. If you already have a Worker, the wizard just asks for its address. The code of a new Worker checks a secret part of the address, which the wizard generates, so strangers cannot use your Worker. The bot token passes through your Cloudflare account.

## Troubleshooting

- **The PC does not wake up from the bot.** Wake-on-LAN works only over a cable. Wake on LAN must be enabled in the BIOS.
- **The bot does not answer.** Check the router log: `tail -f /opt/var/log/pc-pult.log`. If it shows Telegram connection errors, put the Worker address into the `tg_api` field or the proxy address into the `tg_proxy` field of `/opt/etc/pc-pult/config.json` and restart the service: `/opt/etc/init.d/S99pc-pult restart`.
- **The PC is shown as off while it is on.** The service log is `C:\ProgramData\PcPult\logs\service.log`. If the PC cannot reach the router, pair it again: run `/opt/sbin/pc-pult-router -pair` on the router and the new command on the PC.
- **No CPU temperature.** The service could not open the sensors, everything else works.
- **Windows or an antivirus asks about the service.** The program is not signed with a digital signature.

## Updating and removing

To update, run the same command on the router and on the PC, the settings are kept. To connect another PC, run `/opt/sbin/pc-pult-router -pair` on the router.

Remove from the router:

```
wget -qO- https://raw.githubusercontent.com/klaymm/pc-pult/main/install.sh | sh -s remove
```

Remove from the PC: run the install command with `-Remove` instead of `-Router` and `-Code`.

## Limitations

Works only with Keenetic and Netcraze routers with Entware. Tested on a Netcraze with an aarch64 processor and Windows 11; builds for other router processors exist but have not been tried on real devices.

## License

MIT
