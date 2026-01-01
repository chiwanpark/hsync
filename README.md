# hsync

hsync is a lightweight synchronization tool designed for [Heynote](https://heynote.com), enabling seamless synchronization of text notes across multiple devices.
It consists of a central server and client software that communicate via HTTP to keep a directory of text files in sync.

## Features

- **Directory Synchronization:** Syncs multiple `.txt` files within a specified directory.
- **3-Way Merge:** Uses the `diffmatchpatch` algorithm to intelligently merge concurrent edits from multiple clients, minimizing conflicts.
- **HTTP Transport:** communicating over standard HTTP.
- **Shared Key Authentication:** simple security model using a shared secret key between server and clients.
- **Automatic Sync:** Clients automatically detect local changes and push them to the server.

## Installation

### Prerequisites

- [Go](https://go.dev/) 1.18 or higher.

### Build

Clone the repository and build the server and client binaries:

```bash
git clone <repository-url>
cd hsync
go mod tidy
go build -o bin/server ./cmd/server
go build -o bin/client ./cmd/client
```

The binaries will be located in the `bin/` directory.

## Usage

### Server

The server manages the central copy of the notes and handles merge operations.

```bash
./bin/server [flags]
```

**Flags:**
- `-addr`: Address to listen on (default `":8080"`).
- `-dir`: Path to the directory storing the server-side text files (default `"data"`).
- `-key`: Shared secret key for authentication (default `"default-secret"`).

**Example:**
```bash
./bin/server -addr :8080 -dir ./server_notes -key mySecretKey
```

### Client

The client runs on your local machine, monitoring a directory and syncing changes to the server.

```bash
./bin/client [flags]
```

**Flags:**
- `-server`: URL of the hsync server (default `"http://localhost:8080"`).
- `-dir`: Path to the local directory to synchronize (default `"."`).
- `-key`: Shared secret key matching the server (default `"default-secret"`).
- `-interval`: Duration to wait between checks (default `5s`).

**Example:**
```bash
./bin/client -server http://myserver.com:8080 -dir ./my_notes -key mySecretKey -interval 2s
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
bash test.sh
```

