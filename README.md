# hsync

hsync is a lightweight synchronization tool designed for [Heynote](https://heynote.com), enabling seamless synchronization of text notes across multiple devices.
It consists of a central server and client software that communicate via HTTP to keep a directory of text files in sync.

## Features

- **Directory Synchronization:** Syncs multiple `.txt` files within a specified directory, including nested directories.
- **3-Way Merge:** Uses the `diffmatchpatch` algorithm to intelligently merge concurrent edits from multiple clients, minimizing conflicts.
- **HTTP Transport:** communicating over standard HTTP.
- **Shared Key Authentication:** simple security model using a shared secret key between server and clients.
- **Automatic Sync:** Clients automatically detect local changes and push them to the server.
- **Single Binary:** Both server and client functionalities are bundled into a single `hsync` executable.

## Installation

### Prerequisites

- [Go](https://go.dev/) 1.27.1 or higher.

### Build

Clone the repository and build the `hsync` binary:

```bash
git clone <repository-url>
cd hsync
go mod tidy
```

You can build using `make`:

```bash
make build
```

Or manually:

```bash
go build -o bin/hsync ./cmd/hsync
```

The binary will be located in the `bin/` directory.

## Usage

The `hsync` binary uses subcommands to run as either a server or a client.

### Server

The server manages the central copy of the notes and handles merge operations.

```bash
./bin/hsync server [flags]
```

**Flags:**
- `-addr`: Address to listen on (default `":8080"`).
- `-dir`: Path to the directory storing the server-side text files (default `"data"`).
- `-key`: Shared secret key for authentication (default `"default-secret"`).

**Example:**
```bash
./bin/hsync server -addr :8080 -dir ./server_notes -key mySecretKey
```

### Client

The client runs on your local machine, monitoring a directory and syncing changes to the server. Configuration is managed via a TOML file.

```bash
./bin/hsync client [flags]
```

**Flags:**
- `-config`: Path to the configuration file (default: `${HOME}/.config/hsync.toml`).

**Configuration File (`hsync.toml`):**

The client uses a TOML file for configuration. Below is an example:

```toml
server = "http://localhost:8080"      # URL of the hsync server
key = "mySecretKey"                   # Shared secret key matching the server
dir = "./my_notes"                    # Path to the local directory to synchronize
interval = "2s"                       # Duration to wait between checks (e.g., "5s", "1m")
state = "./hsync-state.json"          # Optional path to the sync state file
timeout = "60s"                       # Optional HTTP timeout per request (default "60s")
```

The client stores the last synchronized content of every note in a state file.
It is required to tell a locally deleted or moved note apart from a note that was never synchronized, so the move is not undone on the next start.
When `state` is omitted, the file is created under `${XDG_STATE_HOME:-~/.local/state}/hsync/` with a name derived from the synchronized directory.

**Example:**

Run with default config path (`~/.config/hsync.toml`):
```bash
./bin/hsync client
```

Run with a specific config file:
```bash
./bin/hsync client -config my_config.toml
```

## Docker

You can also run the server using Docker:

```bash
docker build -t hsync .
docker run -p 8080:8080 -v $(pwd)/data:/app/data hsync
```

## How it Works

1. **Initialization:** When the client starts, it loads its state file. On the very first run it downloads the current state of all text files from the server, including files in nested directories. Local notes that the server does not have are uploaded instead of removed.
2. **Monitoring:** The client checks the local files periodically (defined by `interval`).
3. **Syncing:**
   - If a local file is modified, the client sends a patch request to the server.
   - The server performs a 3-way merge (Base vs. Latest vs. Server-Current) and saves the result.
   - The server responds with the merged content.
   - The client updates its local file with the merged result to stay in sync.

**Limitations:**

- Run at most one client per synchronized directory; two clients sharing a directory also share the state file and will fight over it.
- Tombstones are kept for 90 days. A client that is offline longer re-uploads notes that were deleted meanwhile.
- Notes are synchronized byte for byte, so devices that save with different line endings keep rewriting each other's notes.

## Development & Testing

Run the unit tests and the end-to-end script (one server, two clients) with:

```bash
make test
```

You can also run the end-to-end script directly:

```bash
./scripts/test.sh
```