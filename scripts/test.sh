#!/bin/bash
set -e

TEST_DIR=$(mktemp -d -t hsync_test_XXXXXX)
SERVER_DIR="$TEST_DIR/server"
DIR_A="$TEST_DIR/client_a"
DIR_B="$TEST_DIR/client_b"

mkdir -p "$SERVER_DIR" "$DIR_A" "$DIR_B" bin
go build -o bin/hsync ./cmd/hsync

cleanup() {
    kill $SERVER_PID $PID_A $PID_B 2>/dev/null || true
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

check() {
    if eval "$2"; then
        echo "ok: $1"
    else
        echo "FAIL: $1"
        exit 1
    fi
}

start_client() {
    cat <<EOF > "$TEST_DIR/config_$1.toml"
server = "http://localhost:8082"
key = "secret"
dir = "$TEST_DIR/client_$1"
interval = "1s"
state = "$TEST_DIR/state_$1.json"
EOF
    ./bin/hsync client -config "$TEST_DIR/config_$1.toml" > "$TEST_DIR/client_$1.log" 2>&1 &
}

echo "Using $TEST_DIR"
echo "Initial Note 1" > "$SERVER_DIR/note1.txt"
mkdir -p "$SERVER_DIR/projects/alpha"
echo "Initial Nested Note" > "$SERVER_DIR/projects/alpha/nested.txt"

./bin/hsync server -addr :8082 -dir "$SERVER_DIR" -key secret &
SERVER_PID=$!
sleep 1

start_client a
PID_A=$!
start_client b
PID_B=$!
sleep 2

check "clients download the initial notes" \
    '[ -f "$DIR_A/note1.txt" ] && grep -q "Initial Nested Note" "$DIR_B/projects/alpha/nested.txt"'

echo "Edit on A and a new nested note on B..."
echo "Modified by A" > "$DIR_A/note1.txt"
mkdir -p "$DIR_B/projects/beta/deep"
echo "Deep note by B" > "$DIR_B/projects/beta/deep/note4.txt"
sleep 3

check "the edit reaches the server and B" \
    'grep -q "Modified by A" "$SERVER_DIR/note1.txt" && grep -q "Modified by A" "$DIR_B/note1.txt"'
check "the nested note reaches the server and A" \
    'grep -q "Deep note by B" "$SERVER_DIR/projects/beta/deep/note4.txt" && grep -q "Deep note by B" "$DIR_A/projects/beta/deep/note4.txt"'

echo "Move on A..."
mkdir -p "$DIR_A/archive"
mv "$DIR_A/note1.txt" "$DIR_A/archive/note1.txt"
sleep 4

check "the move reaches the server and B" \
    '[ -f "$SERVER_DIR/archive/note1.txt" ] && [ ! -f "$SERVER_DIR/note1.txt" ] && [ -f "$DIR_B/archive/note1.txt" ]'

echo "Restarting A..."
kill $PID_A
wait $PID_A 2>/dev/null || true
start_client a
PID_A=$!
sleep 3

check "the restart does not resurrect the moved note" '[ ! -f "$DIR_A/note1.txt" ]'

echo "Delete on B..."
rm "$DIR_B/archive/note1.txt"
sleep 4

check "the deletion reaches the server and A" \
    '[ ! -f "$SERVER_DIR/archive/note1.txt" ] && [ ! -f "$DIR_A/archive/note1.txt" ]'

echo "Taking B offline for several commits..."
kill $PID_B
wait $PID_B 2>/dev/null || true

for i in 1 2 3; do
    echo "Note $i by A" > "$DIR_A/batch$i.txt"
    sleep 1
done

echo "Offline edit by B" > "$DIR_B/projects/alpha/nested.txt"
start_client b
PID_B=$!
sleep 4

check "the offline edit is merged instead of dropped" \
    'grep -q "Offline edit by B" "$SERVER_DIR/projects/alpha/nested.txt"'
check "B catches up with the commits it missed" '[ -f "$DIR_B/batch3.txt" ]'

echo "Checking rejected pushes..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "X-Sync-Key: secret" \
    -d '{"files":{}}' "http://localhost:8082/push")
check "a push without a parent is rejected with 400" '[ "$STATUS" = "400" ]'

STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "X-Sync-Key: secret" \
    -d '{"parent":"0000000000000000000000000000000000000000000000000000000000000000","files":{}}' \
    "http://localhost:8082/push")
check "a push with an unknown parent is rejected with 409" '[ "$STATUS" = "409" ]'

HEAD_COMMIT=$(curl -s -H "X-Sync-Key: secret" "http://localhost:8082/index" |
    sed -n 's/.*"commit":"\([0-9a-f]*\)".*/\1/p')
ESCAPE_HASH=$(printf 'escaped' | sha256sum | cut -d' ' -f1)
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "X-Sync-Key: secret" \
    -d "{\"parent\":\"$HEAD_COMMIT\",\"files\":{\"../../escape.txt\":\"$ESCAPE_HASH\"},\"blobs\":{\"$ESCAPE_HASH\":\"escaped\"}}" \
    "http://localhost:8082/push")
check "a push with a traversal path is rejected with 400" \
    '[ "$STATUS" = "400" ] && [ ! -f "$TEST_DIR/escape.txt" ]'

echo "All end-to-end checks passed."
