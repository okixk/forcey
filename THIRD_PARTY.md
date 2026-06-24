# Third-party software

forcey does not bundle Microsoft Sysinternals Handle.

When the final force level is required, forcey downloads the appropriate Handle executable from Microsoft's official Sysinternals endpoint, verifies its Microsoft Authenticode signature, and stores it under `%LOCALAPPDATA%\forcey\bin`.

Sysinternals Handle is distributed under Microsoft's own license terms.
