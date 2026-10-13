$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

try {
    $wailsCommand = Get-Command wails -ErrorAction SilentlyContinue
    $userWails = Join-Path $env:USERPROFILE 'go\bin\wails.exe'
    if ($wailsCommand) {
        & $wailsCommand.Source build -skipbindings -trimpath
    } elseif (Test-Path -LiteralPath $userWails) {
        & $userWails build -skipbindings -trimpath
    } else {
        throw 'Wails CLI is required to package the Windows icon and manifest. Install it with: go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0'
    }
    if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed.' }

    # Wails copies its JS runtime even when bindings are skipped. This app uses window.runtime directly.
    $workspace = [System.IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\') + '\'
    $runtimeFolder = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'frontend\wailsjs'))
    if (-not $runtimeFolder.StartsWith($workspace, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw 'Generated runtime cleanup target is outside the workspace.'
    }
    if (Test-Path -LiteralPath $runtimeFolder) {
        $resolved = (Resolve-Path -LiteralPath $runtimeFolder).ProviderPath
        if (-not $resolved.StartsWith($workspace, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw 'Resolved runtime cleanup target is outside the workspace.'
        }
        if ((Get-Item -Force -LiteralPath $resolved).Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
            throw 'Generated runtime cleanup target is a reparse point.'
        }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
    Write-Output 'Build successful: build\bin\bondo.exe'
    exit 0
} catch {
    Write-Error $_
    exit 1
}
