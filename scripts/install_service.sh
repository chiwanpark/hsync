#!/bin/bash
set -e

if [ -z "$1" ]; then
  echo "Usage: $0 <path-to-binary>"
  exit 1
fi

SOURCE_BINARY="$1"

# Detect OS
OS="$(uname -s)"
USER_BIN="$HOME/.local/bin"
BINARY_NAME="hsync"

echo "Detected OS: $OS"

if [ ! -f "$SOURCE_BINARY" ]; then
    echo "Error: Binary file '$SOURCE_BINARY' not found."
    exit 1
fi

echo "Installing binary from $SOURCE_BINARY..."
mkdir -p "$USER_BIN"
cp "$SOURCE_BINARY" "$USER_BIN/$BINARY_NAME"
chmod +x "$USER_BIN/$BINARY_NAME"

# Ensure bin exists in PATH (informational)
if [[ ":$PATH:" != *":$USER_BIN:"* ]]; then
    echo "WARNING: $USER_BIN is not in your PATH."
fi

if [ "$OS" = "Linux" ]; then
    SERVICE_DIR="$HOME/.config/systemd/user"
    SERVICE_FILE="hsync.service"
    SRC_SERVICE="dist/linux/$SERVICE_FILE"

    mkdir -p "$SERVICE_DIR"
    cp "$SRC_SERVICE" "$SERVICE_DIR/"

    # Reload and enable
    systemctl --user daemon-reload
    systemctl --user enable "$SERVICE_FILE"
    systemctl --user restart "$SERVICE_FILE"
    
    echo "Service installed and started. Check status with: systemctl --user status $SERVICE_FILE"

elif [ "$OS" = "Darwin" ]; then
    SERVICE_DIR="$HOME/Library/LaunchAgents"
    SERVICE_FILE="com.chiwanpark.hsync.plist"
    SRC_SERVICE="dist/macos/$SERVICE_FILE"

    mkdir -p "$SERVICE_DIR"
    
    # Replace placeholder with actual username
    sed "s/\${USER}/$USER/g" "$SRC_SERVICE" > "$SERVICE_DIR/$SERVICE_FILE"

    # Load service
    launchctl unload "$SERVICE_DIR/$SERVICE_FILE" 2>/dev/null || true
    launchctl load "$SERVICE_DIR/$SERVICE_FILE"

    echo "Service installed and loaded. Logs at /tmp/hsync.out"
else
    echo "Unsupported OS: $OS"
    exit 1
fi
