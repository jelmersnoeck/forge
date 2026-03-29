# Running Forge Server in Daemon Mode

The forge server supports native daemon mode on all platforms (macOS, Linux, Windows).

## Quick Start

```bash
# Start server in daemon mode (default paths)
just dev-server-daemon

# Or manually:
./forge-server -daemon

# Check logs
just tail-server

# Stop server
just stop-server
```

## How It Works

When you run `./forge-server -daemon`:

1. The server forks itself into a background process
2. Writes its PID to `/tmp/forge/sessions/forge.pid`
3. Redirects all logs to `/tmp/forge/sessions/forge.log`
4. Parent process exits, child continues running
5. Server handles SIGINT/SIGTERM for graceful shutdown

## Command Line Options

```bash
./forge-server -daemon                    # Use defaults
./forge-server -daemon \
  -pid-file /custom/path/forge.pid \
  -log-file /custom/path/forge.log
```

Defaults:
- PID file: `$SESSIONS_DIR/forge.pid` (default: `/tmp/forge/sessions/forge.pid`)
- Log file: `$SESSIONS_DIR/forge.log` (default: `/tmp/forge/sessions/forge.log`)

## Managing the Daemon

### Start
```bash
./forge-server -daemon
# Output:
# forge server started in background (PID: 12345)
#   log file: /tmp/forge/sessions/forge.log
#   pid file: /tmp/forge/sessions/forge.pid
#
# To stop:
#   kill $(cat /tmp/forge/sessions/forge.pid)
```

### Check Status
```bash
# Check if running
ps -p $(cat /tmp/forge/sessions/forge.pid)

# Or test the API
curl http://localhost:3000/sessions
```

### View Logs
```bash
# Follow logs
tail -f /tmp/forge/sessions/forge.log

# Or with just:
just tail-server
```

### Stop
```bash
# Send SIGTERM (graceful shutdown)
kill $(cat /tmp/forge/sessions/forge.pid)

# Or with just:
just stop-server

# Force kill (not recommended)
kill -9 $(cat /tmp/forge/sessions/forge.pid)
```

## Environment Variables

All environment variables work the same in daemon mode:

```bash
# Set in .env file (recommended)
ANTHROPIC_API_KEY=sk-ant-...
GATEWAY_PORT=3000
GATEWAY_HOST=0.0.0.0
WORKSPACE_DIR=/custom/workspace
SESSIONS_DIR=/custom/sessions
AGENT_BIN=forge-agent

# Or export before running
export GATEWAY_PORT=8080
./forge-server -daemon
```

## Platform Support

This implementation works on:
- **macOS** - native Go process forking
- **Linux** - native Go process forking  
- **Windows** - uses same mechanism (process re-execution)

No external dependencies required (systemd, launchd, etc).

## Comparison with Other Methods

| Method | Pros | Cons |
|--------|------|------|
| **`-daemon` flag** | ✅ Platform-agnostic<br>✅ No dependencies<br>✅ Simple PID management | ❌ No auto-restart |
| systemd | ✅ Auto-restart<br>✅ Service management | ❌ Linux only<br>❌ Requires root |
| Docker | ✅ Isolated environment<br>✅ Easy deployment | ❌ Overhead<br>❌ Requires Docker |
| launchd | ✅ Native macOS integration | ❌ macOS only<br>❌ Complex config |
| tmux/screen | ✅ Interactive access | ❌ Manual management<br>❌ Not a true daemon |

## Troubleshooting

### Server won't start in daemon mode
```bash
# Check if port is already in use
lsof -i :3000

# Check permissions on sessions directory
ls -la /tmp/forge/sessions/
```

### Can't find PID file
```bash
# Check SESSIONS_DIR env var
echo $SESSIONS_DIR

# Or search for the file
find /tmp -name forge.pid 2>/dev/null
```

### Logs not appearing
```bash
# Verify log file location
ls -la /tmp/forge/sessions/forge.log

# Check stderr for startup errors (before daemon fork)
./forge-server -daemon 2>&1
```

### Server exits immediately
```bash
# Run in foreground to see errors
./forge-server

# Common issues:
# - Missing forge-agent binary
# - Invalid ANTHROPIC_API_KEY
# - Permission issues
```

## Integration with System Services

### macOS (launchd)
Create `~/Library/LaunchAgents/com.forge.server.plist`:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.forge.server</string>
    <key>ProgramArguments</key>
    <array>
        <string>/path/to/forge-server</string>
        <string>-daemon</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>ANTHROPIC_API_KEY</key>
        <string>your-key-here</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.forge.server.plist
launchctl start com.forge.server
```

### Linux (systemd)
Create `/etc/systemd/system/forge.service`:
```ini
[Unit]
Description=Forge Server
After=network.target

[Service]
Type=simple
User=youruser
WorkingDirectory=/path/to/forge
Environment="ANTHROPIC_API_KEY=your-key"
ExecStart=/path/to/forge-server
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable forge
sudo systemctl start forge
```

Note: With systemd/launchd, don't use the `-daemon` flag since they manage the background process themselves.
