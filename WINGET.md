# Publishing forcey on WinGet

1. Create a public GitHub repository and push this project.
2. Create and push the first release tag:

```powershell
git tag v1.0.0
git push origin v1.0.0
```

3. Wait for the Release workflow to publish `forcey-windows-x64.zip`.
4. Generate, validate, and optionally submit the manifests:

```powershell
.\scripts\prepare-winget.ps1 -Repository "YOUR_GITHUB_USERNAME/forcey"
winget install Microsoft.WingetCreate
wingetcreate submit .\winget\manifests\o\okix\forcey\1.0.0
```

Or generate and submit in one step:

```powershell
.\scripts\prepare-winget.ps1 -Repository "YOUR_GITHUB_USERNAME/forcey" -Submit
```

The package identifier is `okix.forcey`. Keep it unchanged after the first accepted submission.
