# battymon

A TUI battery monitor for macOS and Linux.

`battymon` is a clean TUI for checking battery state, power data, health, and runtime information without digging through raw command output. It is designed to work well both on full-size systems and small terminal-first devices like the Clockwork Pi uConsole.

## Features

- macOS and Linux support
- Terminal UI with keyboard-only controls
- Battery percentage and status
- AC power detection
- Power draw, voltage, and current
- Current, full, and design capacity
- Battery health
- Cycle count
- Temperature
- ETA / time remaining
- Linux extras:
  - brightness
  - CPU governor
  - Wi-Fi state
  - Bluetooth state
- Aggregated handling for multi-battery Linux systems

## Screenshots

### macOS
<img src="https://github.com/skellywampus/battymon/blob/main/macOS_screenshot.PNG" alt="">

### Linux on uConsole
<img src="https://github.com/skellywampus/battymon/blob/main/uConsole_screenshot.PNG" alt="">

## Data sources

### macOS
- `pmset -g batt`
- `ioreg -r -c AppleSmartBattery`

### Linux
- `/sys/class/power_supply`
- `/sys/class/backlight`
- `/sys/class/rfkill`
- `/sys/devices/system/cpu/.../cpufreq`

## Build

```
git clone https://github.com/yourusername/battymon.git
cd battymon
go mod tidy
go build -o battymon
```

## Run

```
./battymon
```

## Controls

- `q` quit
- `r` refresh

## Install

Prebuilt binaries are available through GitHub Releases.

Install flow:

### macOS Apple Silicon
```
curl -L -o battymon https://github.com/skellywampus/battymon/releases/download/Release/battymon-darwin-arm64
chmod +x battymon
sudo mv battymon /usr/local/bin/
```

### Linux x86_64
```
curl -L -o battymon https://github.com/skellywampus/battymon/releases/download/Release/battymon-linux-amd64
chmod +x battymon
sudo mv battymon /usr/local/bin/
```

### Linux ARM64
```
curl -L -o battymon https://github.com/skellywampus/battymon/releases/download/Release/battymon-linux-arm64
chmod +x battymon
sudo mv battymon /usr/local/bin/
```

### Running Post-Install
```
battymon
```

## Notes

- Some fields may be unavailable depending on the hardware and OS.
- Linux battery reporting varies by device and kernel support.
- `battymon` derives some values when they are not directly exposed.

## Tested on

- MacBook Air M2
- Clockwork Pi uConsole
- x86_64 Fedora laptop (2011 MacBook Pro)

## Goals

- fast to read
- lightweight
- useful on small screens

## License

MIT
