# hsync

hsync is a lightweight synchronization tool designed for [Heynote](https://heynote.com), enabling seamless synchronization of text notes across multiple devices.
It consists of a central server and client software that communicate via HTTP to keep a directory of text files in sync.

## Features

- **Directory Synchronization:** Syncs multiple `.txt` files within a specified directory.
- **3-Way Merge:** Uses the `diffmatchpatch` algorithm to intelligently merge concurrent edits from multiple clients, minimizing conflicts.
- **HTTP Transport:** communicating over standard HTTP.
- **Shared Key Authentication:** simple security model using a shared secret key between server and clients.
- **Automatic Sync:** Clients automatically detect local changes and push them to the server.
- **Single Binary:** Both server and client functionalities are bundled into a single `hsync` executable.

## Installation

### Prerequisites

- [Go](https://go.dev/) 1.25.3 or higher.

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
```

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

1. **Initialization:** When the client starts, it downloads the current state of all text files from the server.
2. **Monitoring:** The client checks the local files periodically (defined by `-interval`).
3. **Syncing:**
   - If a local file is modified, the client sends a patch request to the server.
   - The server performs a 3-way merge (Base vs. Latest vs. Server-Current) and saves the result.
   - The server responds with the merged content.
   - The client updates its local file with the merged result to stay in sync.

## Development & Testing

You can run the provided test script to simulate a sync session with one server and two clients:

```bash
./scripts/test.sh
```