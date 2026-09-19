# hsync

hsync keeps a directory of text notes in sync across devices, built for [Heynote](https://heynote.com).
A server holds the notes and their history, and a client on every device pushes local changes and checks out the result.

## Features

- Syncs `.txt` files in a directory, including nested directories.
- Keeps a commit history, so a client that was offline for a long time merges against the state it knows instead of overwriting newer work.
- Merges concurrent edits with `diffmatchpatch`, and propagates moves and deletions instead of resurrecting notes.
- Runs as a single binary with shared key authentication over HTTP.

## Build

Requires [Go](https://go.dev/) 1.27.1 or higher.

```bash
make build          # or: go build -o bin/hsync ./cmd/hsync
```

## Server

```bash
./bin/hsync server -addr :8080 -dir ./server_notes -key mySecretKey
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-addr` | `:8080` | Address to listen on |
| `-dir` | `data` | Directory holding the notes and their history |
| `-key` | `default-secret` | Shared secret the clients must send |

The notes stay plain `.txt` files in `-dir`; the history lives next to them in `.hsync/`.
Notes you add, edit or delete in that directory by hand are committed on the next request.

Docker works as well:

```bash
docker build -t hsync .
docker run -p 8080:8080 -v $(pwd)/data:/app/data hsync
```

## Client

```bash
./bin/hsync client -config my_config.toml
```

Without `-config` the client reads `~/.config/hsync.toml`:

```toml
server = "http://localhost:8080"   # URL of the hsync server, may include a subpath
key = "mySecretKey"                # Shared secret matching the server
dir = "./my_notes"                 # Directory to synchronize
interval = "2s"                    # Time between sync cycles (default "5s")
timeout = "60s"                    # HTTP timeout per request (default "60s")
state = "./hsync-state.json"       # Sync state file (default under $XDG_STATE_HOME/hsync)
```

The state file records the commit the device last synchronized with and the hash of every note.
That commit is the parent of the next push and tells a note deleted or moved locally apart from one that was never synchronized.

## Behind a Reverse Proxy

The server always serves `/index`, `/push` and `/blob` at the root, so the proxy has to strip its own path prefix.
Point the client at the full URL including that prefix, for example `server = "https://example.com/heynote"`.

```nginx
location ^~ /heynote/ {
    proxy_pass http://127.0.0.1:8080/;
    client_max_body_size 512m;
}
```

A `rewrite` works as well, and is the form to use when other directives in the block prevent a trailing slash on `proxy_pass`:

```nginx
location ^~ /heynote/ {
    rewrite /heynote/(.*) /$1 break;
    proxy_pass http://127.0.0.1:8080;
    client_max_body_size 512m;
}
```

Raise the body limit, since a push carries the notes that changed.

## Limitations

- Run at most one client per synchronized directory; two clients sharing a directory also share the state file.
- The history is never pruned, so the data directory keeps a blob for every version of every note.
- Deleting the server history makes clients push against a fresh root commit, which merges notes that differ instead of tracking a common ancestor.
- Notes are synchronized byte for byte, so devices saving with different line endings keep rewriting each other's notes.

## Development

```bash
make test           # unit tests and the end-to-end script
./scripts/test.sh   # end-to-end only: one server, two clients
```
