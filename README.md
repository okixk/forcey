# forcey ✨

forcey is a small Windows command-line tool for permanently deleting stubborn files and folders.

It starts with a normal deletion and only escalates when necessary: attributes, administrator rights, permissions, ownership, and finally locked handles.

## Install

Once published on WinGet:

```powershell
winget install okix.forcey
```

For a manual install, download `forcey-windows-x64.zip`, extract `forcey.exe`, and place it in a directory on your `PATH`.

## Usage

```powershell
forcey .\folder
forcey .\folder1 .\folder2 ".\file with spaces.txt"
forcey C:\Temp\stubborn-folder --yes
forcey .\folder --dry-run
```

Run `forcey --help` for all options.

Deletion is permanent and bypasses the Recycle Bin. forcey protects critical locations by default and only requests elevation when required.

## Build

```powershell
.\build.ps1
```

The release archive is written to `dist\forcey-windows-x64.zip`.

## License

Copyright (C) 2026 okix.

GNU Affero General Public License v3.0 only. See [LICENSE](LICENSE).
