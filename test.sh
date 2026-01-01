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

# Cleanup
pkill -f bin/server || true
pkill -f bin/client || true

# Setup initial server data
echo "Initial Note 1" > "$SERVER_DATA_DIR/note1.txt"
echo "Initial Note 2" > "$SERVER_DATA_DIR/note2.txt"

# Start Server
echo "Starting Server..."
./bin/server -addr :8082 -dir "$SERVER_DATA_DIR" -key secret &
SERVER_PID=$!
sleep 1

# Start Client A
echo "Starting Client A..."
./bin/client -server http://localhost:8082 -key secret -dir "$CLIENT_A_DIR" -interval 1s > "$LOG_A" 2>&1 &
CLIENT_A_PID=$!
sleep 1

# Verify A downloaded files
if [ -f "$CLIENT_A_DIR/note1.txt" ] && [ -f "$CLIENT_A_DIR/note2.txt" ]; then
    echo "Client A downloaded initial files."
else
    echo "Client A failed to download files."
    exit 1
fi

# Start Client B
echo "Starting Client B..."
./bin/client -server http://localhost:8082 -key secret -dir "$CLIENT_B_DIR" -interval 1s > "$LOG_B" 2>&1 &
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

# Cleanup
kill $SERVER_PID $CLIENT_A_PID $CLIENT_B_PID
rm -rf "$TEST_DIR"