# home-server

A simple file server for sharing and managing files with authentication.

## Termux Storage Setup (Android Users)

If you're running this on Termux and files are not visible in your Android file manager, follow these steps:

### 1. Grant Termux Storage Permissions

Run this command in Termux:
```bash
termux-setup-storage
```

This will:
- Request storage permissions from Android
- Create symlinks to your shared storage at `$HOME/storage/shared`
- Create symlinks to external SD card at `$HOME/storage/external-1` (if available)

### 2. Check Where Files Are Stored

The server automatically detects the best storage location:

- **External SD Card** (if available): `$HOME/storage/external-1/home-server`
  - Visible in file managers at `/storage/XXXX-XXXX/home-server` (where XXXX-XXXX is your SD card ID)

- **Internal Shared Storage**: `$HOME/storage/shared/home-server`
  - Visible in file managers at `/storage/emulated/0/home-server`

- **Termux Local Storage** (if no permissions): `$HOME/data`
  - **NOT visible** in Android file managers (only accessible within Termux)

### 3. Verify Storage Location

When you start the server, check the logs for where files are stored:
```bash
Files stored at: /data/data/com.termux/files/home/storage/shared/home-server (Internal Shared Storage)
NOTE: Files stored here should be visible in Android file managers at /storage/emulated/0/home-server
```

### 4. Troubleshooting

**If files are not visible:**
1. Run `termux-setup-storage` again
2. Restart Termux completely
3. Check if the `home-server` folder exists in your file manager
4. Try accessing files through the web interface first to confirm upload worked

**If you see "Termux storage not accessible":**
- You need to run `termux-setup-storage` first
- Files will only be accessible within Termux until you grant permissions

### 5. Accessing Files from File Manager

After granting permissions:
- Open your file manager app
- Navigate to `/storage/emulated/0/home-server` (internal storage)
- Or `/storage/XXXX-XXXX/home-server` (external SD card)
- You should see all uploaded files there

## Running the Server

```bash
# Build and run
go build -o home-server
./home-server

# Or run directly
go run main.go
```

## Features

- User authentication (signup/login)
- File upload (single and multiple files)
- File management (rename, delete, share)
- Directory browsing
- Dark theme UI

## Configuration

Set environment variables:
- `STORAGE_PATH`: Custom storage location
- `PORT`: Custom port (default: 8080)

Example:
```bash
export STORAGE_PATH=/sdcard/my-files
export PORT=:9090
./home-server
```