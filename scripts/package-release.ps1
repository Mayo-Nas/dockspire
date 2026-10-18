param(
    [string]$Go = 'go',
    [string]$Git = 'git',
    [string]$Tar = 'tar.exe',
    [string]$Workspace = '',
    [string]$Version = ''
)

$ErrorActionPreference = 'Stop'
if ($Version -and $Version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$') { throw 'Version must look like v0.1.0 or v0.1.0-rc.1' }
$taskRoot = Split-Path -Parent $PSScriptRoot
$taskGitDirectory = $taskRoot.Replace('\', '/')
if (-not $Workspace) { $Workspace = Join-Path $taskRoot '.local' }
$taskWorkspace = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Workspace)
$taskOutput = Join-Path $taskWorkspace 'artifacts\preview'
$taskReleases = Join-Path $taskWorkspace 'artifacts\releases'
$taskEncoding = New-Object System.Text.UTF8Encoding($false)
New-Item -ItemType Directory -Path $taskOutput -Force | Out-Null
New-Item -ItemType Directory -Path $taskReleases -Force | Out-Null

Push-Location $taskRoot
try {
    $taskChanges = & $Git -c "safe.directory=$taskGitDirectory" status --porcelain
    if ($LASTEXITCODE -ne 0) { throw 'Could not inspect Git status' }
    if ($taskChanges) { throw 'Commit working changes before packaging a preview' }
    $taskCommit = & $Git -c "safe.directory=$taskGitDirectory" rev-parse HEAD
    if ($LASTEXITCODE -ne 0) { throw 'Could not identify source commit' }

    $env:GOCACHE = Join-Path $taskWorkspace 'cache\go-build'
    $env:GOPATH = Join-Path $taskWorkspace 'cache\gopath'
    $env:GOTMPDIR = Join-Path $taskWorkspace 'cache\tmp'
    $env:GOENV = 'off'
    $env:GOTOOLCHAIN = 'local'
    $env:GOTELEMETRY = 'off'
    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    New-Item -ItemType Directory -Path $env:GOTMPDIR -Force | Out-Null
    # RELEASE.txt records the commit; avoid a second Git probe under another Windows account.
    & $Go build -buildvcs=false -trimpath '-ldflags=-s -w' -o (Join-Path $taskOutput 'stacklane') ./cmd/dockspire
    if ($LASTEXITCODE -ne 0) { throw 'Linux build failed' }
    Copy-Item -LiteralPath (Join-Path $taskRoot 'deploy\legacy-preview\Dockerfile') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'deploy\legacy-preview\README.md') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'deploy\legacy-preview\compose.yaml') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'deploy\legacy-preview\compose.portainer.yaml') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'deploy\legacy-preview\compose.host.yaml') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'deploy\legacy-preview\stacklane-host.service') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'scripts\install-host-helper.sh') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'scripts\diagnose-host-refresh.sh') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'scripts\repair-host-refresh.sh') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'scripts\restrict-host-helper.sh') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'scripts\check-installation.sh') -Destination $taskOutput -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'scripts\install-preview.sh') -Destination $taskOutput -Force
    New-Item -ItemType Directory -Path (Join-Path $taskOutput 'licenses') -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $taskRoot 'licenses\app-third-party.txt') -Destination (Join-Path $taskOutput 'licenses') -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'licenses\yaml-LICENSE') -Destination (Join-Path $taskOutput 'licenses') -Force
    Copy-Item -LiteralPath (Join-Path $taskRoot 'licenses\Apache-2.0.txt') -Destination (Join-Path $taskOutput 'licenses') -Force

    Copy-Item -LiteralPath (Join-Path $taskRoot 'scripts\install.sh') -Destination $taskOutput -Force
    $taskFiles = 'install.sh', 'stacklane', 'Dockerfile', 'README.md', 'compose.yaml', 'compose.portainer.yaml', 'compose.host.yaml', 'stacklane-host.service', 'install-host-helper.sh', 'diagnose-host-refresh.sh', 'repair-host-refresh.sh', 'restrict-host-helper.sh', 'check-installation.sh', 'install-preview.sh', 'licenses/yaml-LICENSE', 'licenses/Apache-2.0.txt', 'licenses/app-third-party.txt'
    if ($Version) {
        # Normal releases contain only the default installation, not legacy helper/import overrides.
        $taskFiles = 'stacklane', 'Dockerfile', 'README.md', 'compose.yaml', 'install.sh', 'install-preview.sh', 'check-installation.sh', 'licenses/yaml-LICENSE', 'licenses/Apache-2.0.txt', 'licenses/app-third-party.txt'
    }
    $taskChecksums = foreach ($taskFile in $taskFiles) {
        $taskHasher = [System.Security.Cryptography.SHA256]::Create()
        $taskStream = [System.IO.File]::OpenRead((Join-Path $taskOutput $taskFile))
        try {
            $taskHash = [System.BitConverter]::ToString($taskHasher.ComputeHash($taskStream)).Replace('-', '').ToLowerInvariant()
        } finally {
            $taskStream.Dispose()
            $taskHasher.Dispose()
        }
        "$taskHash  $taskFile"
    }
    [System.IO.File]::WriteAllText((Join-Path $taskOutput 'SHA256SUMS'), ($taskChecksums -join "`n") + "`n", $taskEncoding)
    [System.IO.File]::WriteAllText((Join-Path $taskOutput 'RELEASE.txt'), "source_commit=$taskCommit`ntarget=linux/amd64`nversion=$Version`n", $taskEncoding)
    $taskArchive = Join-Path $taskReleases "stacklane-preview-$($taskCommit.Substring(0,7))-linux-amd64.tar"
    $taskTarMode = '-cf'
    if ($Version) { $taskArchive = Join-Path $taskReleases "dockspire-$Version-linux-amd64.tar.gz"; $taskTarMode = '-czf' }
    $taskArchiveFiles = $taskFiles + @('SHA256SUMS', 'RELEASE.txt')
    & $Tar $taskTarMode $taskArchive -C $taskOutput @taskArchiveFiles
    if ($LASTEXITCODE -ne 0) { throw 'Preview archive creation failed' }
    $taskArchiveHasher = [Security.Cryptography.SHA256]::Create()
    $taskArchiveStream = [IO.File]::OpenRead($taskArchive)
    try { $taskArchiveHash = [BitConverter]::ToString($taskArchiveHasher.ComputeHash($taskArchiveStream)).Replace('-', '').ToLowerInvariant() } finally { $taskArchiveStream.Dispose(); $taskArchiveHasher.Dispose() }
    [IO.File]::WriteAllText($taskArchive + '.sha256', "$taskArchiveHash  $([IO.Path]::GetFileName($taskArchive))`n", $taskEncoding)
    Write-Output $taskArchive
    Write-Output ($taskArchive + '.sha256')
} finally {
    Pop-Location
}
