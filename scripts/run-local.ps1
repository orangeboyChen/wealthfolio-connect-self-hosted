[CmdletBinding()]
param(
    [string]$EnvFile,
    [switch]$ValidateOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repositoryRoot = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($EnvFile)) {
    $EnvFile = Join-Path $repositoryRoot ".env"
} elseif (-not [IO.Path]::IsPathRooted($EnvFile)) {
    $EnvFile = Join-Path $repositoryRoot $EnvFile
}

if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) {
    throw "Environment file not found: $EnvFile"
}

function ConvertFrom-DotEnvValue {
    param([string]$RawValue)

    $value = $RawValue.Trim()
    $quote = [char]0
    for ($index = 0; $index -lt $value.Length; $index++) {
        $character = $value[$index]
        if (($character -eq '"' -or $character -eq "'") -and
            ($index -eq 0 -or $value[$index - 1] -ne '\')) {
            if ($quote -eq [char]0) {
                $quote = $character
            } elseif ($quote -eq $character) {
                $quote = [char]0
            }
            continue
        }
        if ($character -eq '#' -and $quote -eq [char]0 -and
            ($index -eq 0 -or [char]::IsWhiteSpace($value[$index - 1]))) {
            $value = $value.Substring(0, $index).TrimEnd()
            break
        }
    }

    if ($value.Length -ge 2) {
        $first = $value[0]
        $last = $value[$value.Length - 1]
        if (($first -eq '"' -and $last -eq '"') -or
            ($first -eq "'" -and $last -eq "'")) {
            return $value.Substring(1, $value.Length - 2)
        }
    }
    return $value
}

$loadedKeys = [System.Collections.Generic.HashSet[string]]::new(
    [System.StringComparer]::OrdinalIgnoreCase
)
$lineNumber = 0
foreach ($rawLine in Get-Content -LiteralPath $EnvFile) {
    $lineNumber++
    $line = $rawLine.Trim()
    if ([string]::IsNullOrWhiteSpace($line) -or $line.StartsWith("#")) {
        continue
    }
    if ($line.StartsWith("export ", [StringComparison]::OrdinalIgnoreCase)) {
        $line = $line.Substring(7).TrimStart()
    }
    $separator = $line.IndexOf('=')
    if ($separator -lt 1) {
        throw "Invalid .env entry at line ${lineNumber}: expected KEY=VALUE"
    }
    $key = $line.Substring(0, $separator).Trim()
    if ($key -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
        throw "Invalid environment variable name at line ${lineNumber}: $key"
    }
    $value = ConvertFrom-DotEnvValue $line.Substring($separator + 1)
    [Environment]::SetEnvironmentVariable($key, $value, "Process")
    [void]$loadedKeys.Add($key)
}

$requiredKeys = @(
    "DATABASE_URL",
    "JWT_SECRET",
    "CONNECT_AUTH_PUBLISHABLE_KEY",
    "ALLOWED_EMAILS"
)
$snapTradeEnabled = [Environment]::GetEnvironmentVariable(
    "SNAPTRADE_ENABLED",
    "Process"
)
if ($snapTradeEnabled -eq "true") {
    $requiredKeys += "SNAPTRADE_CLIENT_ID", "SNAPTRADE_CONSUMER_KEY"
    $authMode = [Environment]::GetEnvironmentVariable(
        "SNAPTRADE_AUTH_MODE",
        "Process"
    )
    if ($authMode -eq "commercial") {
        $requiredKeys += "SNAPTRADE_USER_ID", "SNAPTRADE_USER_SECRET"
    }
}
$ibkrFlexEnabled = [Environment]::GetEnvironmentVariable(
    "IBKR_FLEX_ENABLED",
    "Process"
)
if ($ibkrFlexEnabled -eq "true") {
    $requiredKeys += "IBKR_ACCOUNT_ID", "IBKR_FLEX_TOKEN", "IBKR_FLEX_QUERY_ID"
}

$missingKeys = @(foreach ($key in $requiredKeys) {
    $value = [Environment]::GetEnvironmentVariable($key, "Process")
    if ([string]::IsNullOrWhiteSpace($value) -or
        $value -match '(?i)CHANGE_ME|replace-me') {
        $key
    }
})
if ($missingKeys.Count -gt 0) {
    throw "Missing or placeholder values in .env: $($missingKeys -join ', ')"
}

Write-Host "Loaded $($loadedKeys.Count) variables from $EnvFile."
if ($ValidateOnly) {
    Write-Host "Environment validation passed."
    exit 0
}
if ($null -eq (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go was not found in PATH. Reopen PowerShell after installing Go."
}
Write-Host "Starting Wealthfolio Connect on port $env:SERVER_PORT ..."

Push-Location $repositoryRoot
try {
    & go run ./cmd/server
    if ($LASTEXITCODE -ne 0) {
        throw "Wealthfolio Connect exited with code $LASTEXITCODE"
    }
} finally {
    Pop-Location
}
