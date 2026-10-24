$ErrorActionPreference = 'Stop'
$taskRepo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Push-Location $taskRepo
try {
    $taskSource = Get-Content -LiteralPath 'main.go' -Raw
    if ($taskSource -notmatch 'const version = "([0-9]+\.[0-9]+\.[0-9]+)"') {
        throw 'Cannot determine the application version.'
    }
    $taskVersion = $Matches[1]
    $taskDist = Join-Path $taskRepo 'dist'
    New-Item -ItemType Directory -Path $taskDist -Force | Out-Null
    $taskExe = Join-Path $taskDist 'claude-account-workbench-windows-amd64.exe'
    & go build -tags production -trimpath -ldflags '-H=windowsgui -s -w' -o $taskExe .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
    $taskZip = Join-Path $taskDist "claude-account-workbench-v$taskVersion-windows-amd64.zip"
    $taskFiles = @($taskExe)
    foreach ($taskName in @('README.md','README.en.md','CONTRIBUTING.md','SECURITY.md','CHANGELOG.md','CODE_OF_CONDUCT.md','LICENSE','NOTICE','THIRD_PARTY_LICENSES','docs')) {
        $taskFiles += Join-Path $taskRepo $taskName
    }
    Compress-Archive -LiteralPath $taskFiles -DestinationPath $taskZip -Force
    $taskHashes = foreach ($taskFile in @($taskExe,$taskZip)) {
        $taskHash = Get-FileHash -LiteralPath $taskFile -Algorithm SHA256
        '{0}  {1}' -f $taskHash.Hash.ToLower(),[IO.Path]::GetFileName($taskFile)
    }
    $taskHashes | Set-Content -LiteralPath (Join-Path $taskDist 'SHA256SUMS.txt') -Encoding ascii
    Write-Output "Built Windows package v$taskVersion in dist."
} finally {
    Pop-Location
}
