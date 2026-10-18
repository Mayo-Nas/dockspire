param(
    [string]$Go = 'go',
    [string]$Git = 'git',
    [string]$Tar = 'tar.exe',
    [string]$Workspace = ''
)

# Compatibility entry point for existing preview deployments.
& (Join-Path $PSScriptRoot 'package-release.ps1') -Go $Go -Git $Git -Tar $Tar -Workspace $Workspace
