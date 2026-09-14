#!/bin/bash
set -e

TEST_DIR=$(mktemp -d -t hsync_dir_test_XXXXXX)
echo "Using test directory: $TEST_DIR"

SERVER_DATA_DIR="$TEST_DIR/server_data"
CLIENT_A_DIR="$TEST_DIR/client_a_data"
CLIENT_B_DIR="$TEST_DIR/client_b_data"
LOG_A="$TEST_DIR/client_a.log"
LOG_B="$TEST_DIR/client_b.log"

mkdir -p "$SERVER_DATA_DIR" "$CLIENT_A_DIR" "$CLIENT_B_DIR"

# Build the binary
mkdir -p bin
go build -o bin/hsync ./cmd/hsync

# Cleanup
pkill -f "hsync server" || true
pkill -f "hsync client" || true

# Setup initial server data
echo "Initial Note 1" > "$SERVER_DATA_DIR/note1.txt"
echo "Initial Note 2" > "$SERVER_DATA_DIR/note2.txt"
mkdir -p "$SERVER_DATA_DIR/projects/alpha"
echo "Initial Nested Note" > "$SERVER_DATA_DIR/projects/alpha/nested.txt"

# Start Server
echo "Starting Server..."
./bin/hsync server -addr :8082 -dir "$SERVER_DATA_DIR" -key secret &
SERVER_PID=$!
sleep 1

# Generate Config for Client A
CONFIG_A="$TEST_DIR/config_a.toml"
cat <<EOF > "$CONFIG_A"
server = "http://localhost:8082"
key = "secret"
dir = "$CLIENT_A_DIR"
interval = "1s"
state = "$TEST_DIR/state_a.json"
EOF

# Start Client A
echo "Starting Client A..."
./bin/hsync client -config "$CONFIG_A" > "$LOG_A" 2>&1 &
CLIENT_A_PID=$!
sleep 1

# Verify A downloaded files
if [ -f "$CLIENT_A_DIR/note1.txt" ] && [ -f "$CLIENT_A_DIR/note2.txt" ]; then
    echo "Client A downloaded initial files."
else
    echo "Client A failed to download files."
    exit 1
fi

# Verify A downloaded nested files
if grep -q "Initial Nested Note" "$CLIENT_A_DIR/projects/alpha/nested.txt"; then
    echo "Client A downloaded initial nested file."
else
    echo "Client A failed to download nested file."
    exit 1
fi

# Generate Config for Client B
CONFIG_B="$TEST_DIR/config_b.toml"
cat <<EOF > "$CONFIG_B"
server = "http://localhost:8082"
key = "secret"
dir = "$CLIENT_B_DIR"
interval = "1s"
state = "$TEST_DIR/state_b.json"
EOF

# Start Client B
echo "Starting Client B..."
./bin/hsync client -config "$CONFIG_B" > "$LOG_B" 2>&1 &
CLIENT_B_PID=$!
sleep 1

# Scenario 1: Modify existing file
echo "Client A modifies note1.txt..."
echo "Modified by A" > "$CLIENT_A_DIR/note1.txt"
sleep 2

# Verify Server updated
if grep -q "Modified by A" "$SERVER_DATA_DIR/note1.txt"; then
    echo "Server received update for note1.txt"
else
    echo "Server failed to update note1.txt"
    exit 1
fi

# Scenario 2: Create new file on B
echo "Client B creates note3.txt..."
echo "Created by B" > "$CLIENT_B_DIR/note3.txt"
sleep 2

# Verify Server has new file
if [ -f "$SERVER_DATA_DIR/note3.txt" ]; then
    echo "Server received new file note3.txt"
else
    echo "Server failed to receive note3.txt"
    exit 1
fi

# Scenario 3: Merge conflict
echo "Client A modifies note2.txt"
echo "Change A" > "$CLIENT_A_DIR/note2.txt"
sleep 2

echo "Client B modifies note2.txt"
echo "Change B" > "$CLIENT_B_DIR/note2.txt"
sleep 2

echo "Server content for note2.txt:"
cat "$SERVER_DATA_DIR/note2.txt"

# Scenario 4: Nested file propagation between clients
if grep -q "Initial Nested Note" "$CLIENT_B_DIR/projects/alpha/nested.txt"; then
    echo "Client B downloaded initial nested file."
else
    echo "Client B failed to download nested file."
    exit 1
fi

echo "Client A modifies nested file..."
echo "Nested modified by A" > "$CLIENT_A_DIR/projects/alpha/nested.txt"
sleep 2

if grep -q "Nested modified by A" "$SERVER_DATA_DIR/projects/alpha/nested.txt"; then
    echo "Server received update for nested file."
else
    echo "Server failed to update nested file."
    exit 1
fi

if grep -q "Nested modified by A" "$CLIENT_B_DIR/projects/alpha/nested.txt"; then
    echo "Client B received update for nested file."
else
    echo "Client B failed to receive nested file update."
    exit 1
fi

# Scenario 5: Create new nested file on B
echo "Client B creates a deeply nested file..."
mkdir -p "$CLIENT_B_DIR/projects/beta/deep"
echo "Deep note by B" > "$CLIENT_B_DIR/projects/beta/deep/note4.txt"
sleep 2

if grep -q "Deep note by B" "$SERVER_DATA_DIR/projects/beta/deep/note4.txt"; then
    echo "Server received new nested file projects/beta/deep/note4.txt"
else
    echo "Server failed to receive new nested file."
    exit 1
fi

if grep -q "Deep note by B" "$CLIENT_A_DIR/projects/beta/deep/note4.txt"; then
    echo "Client A downloaded new nested file."
else
    echo "Client A failed to download new nested file."
    exit 1
fi

echo "Client A moves note3.txt into notes/archive/..."
mkdir -p "$CLIENT_A_DIR/notes/archive"
mv "$CLIENT_A_DIR/note3.txt" "$CLIENT_A_DIR/notes/archive/note3.txt"
sleep 4

if [ -f "$SERVER_DATA_DIR/notes/archive/note3.txt" ] && [ ! -f "$SERVER_DATA_DIR/note3.txt" ]; then
    echo "Server moved note3.txt into notes/archive."
else
    echo "Server failed to move note3.txt."
    exit 1
fi

if [ -f "$CLIENT_B_DIR/notes/archive/note3.txt" ] && [ ! -f "$CLIENT_B_DIR/note3.txt" ]; then
    echo "Client B moved note3.txt into notes/archive."
else
    echo "Client B failed to move note3.txt."
    exit 1
fi

echo "Restarting Client A to check the move is not resurrected..."
kill $CLIENT_A_PID
wait $CLIENT_A_PID 2>/dev/null || true
./bin/hsync client -config "$CONFIG_A" > "$LOG_A" 2>&1 &
CLIENT_A_PID=$!
sleep 3

if [ -f "$CLIENT_A_DIR/note3.txt" ]; then
    echo "Client A resurrected the moved note."
    exit 1
else
    echo "Client A kept the move after restart."
fi

echo "Client B deletes the moved note..."
rm "$CLIENT_B_DIR/notes/archive/note3.txt"
sleep 4

if [ -f "$SERVER_DATA_DIR/notes/archive/note3.txt" ]; then
    echo "Server kept a deleted note."
    exit 1
else
    echo "Server removed the deleted note."
fi

if [ -f "$CLIENT_A_DIR/notes/archive/note3.txt" ]; then
    echo "Client A kept a deleted note."
    exit 1
else
    echo "Client A removed the deleted note."
fi

echo "Both clients move note1.txt into shared/..."
mkdir -p "$CLIENT_A_DIR/shared" "$CLIENT_B_DIR/shared"
mv "$CLIENT_A_DIR/note1.txt" "$CLIENT_A_DIR/shared/note1.txt"
mv "$CLIENT_B_DIR/note1.txt" "$CLIENT_B_DIR/shared/note1.txt"
sleep 5

MOVED_LINES=$(wc -l < "$SERVER_DATA_DIR/shared/note1.txt")
if [ "$MOVED_LINES" -eq 1 ]; then
    echo "Server kept a single copy of the moved note."
else
    echo "Server duplicated the moved note ($MOVED_LINES lines)."
    cat "$SERVER_DATA_DIR/shared/note1.txt"
    exit 1
fi

# Scenario 6: Reject path traversal attempts
echo "Checking path traversal rejection..."
TRAVERSAL_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Sync-Key: secret" \
    "http://localhost:8082/sync?filename=..%2F..%2Fescape.txt")
if [ "$TRAVERSAL_STATUS" = "404" ] || [ "$TRAVERSAL_STATUS" = "400" ]; then
    echo "Server rejected traversal request (status $TRAVERSAL_STATUS)."
else
    echo "Server did not reject traversal request (status $TRAVERSAL_STATUS)."
    exit 1
fi
if [ -f "$TEST_DIR/escape.txt" ]; then
    echo "Traversal escaped the data directory."
    exit 1
fi

# Cleanup
kill $SERVER_PID $CLIENT_A_PID $CLIENT_B_PID
rm -rf "$TEST_DIR"