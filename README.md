# dftray

A systray app on Linux for showing the available disk space.

## Prerequisites

At runtime the app requires:

- A system tray that supports the StatusNotifierItem / AppIndicator spec. Older trays may need a proxy like [snixembed](https://git.sr.ht/~steef/snixembed).
- `notify-send` for desktop notifications when the available space drops below the limit (e.g. package `libnotify-bin` on Debian or Ubuntu).

See also [systray](https://pkg.go.dev/fyne.io/systray#section-readme)

## Installation

```sh
go install github.com/ErikKalkoken/dftray@latest
```

## Usage

```sh
dftray [flags]
```

| Flag         | Default | Description                                  |
| ------------ | ------- | -------------------------------------------- |
| `-interval`  | `10`    | update interval in seconds                   |
| `-limit`     | `10`    | lower storage limit in GB (0 disables alert) |
| `-log-level` | `warn`  | log level: debug, info, warn, error          |
| `-path`      | `/`     | path used to query disk stats                |
