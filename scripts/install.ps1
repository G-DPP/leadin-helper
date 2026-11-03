# Installs the LeadIn helper from its GitHub release: downloads the binary
# and SHA256SUMS, checks the hash, runs --install.
#
#   irm https://<LeadIn server>/api/v1/helper/install.ps1 | iex
#
# The release build writes its own version below (scripts/build-release.sh).
# A file downloaded by PowerShell has no Mark of the Web: SmartScreen does
# not stop it (F0 results). Everything runs inside a script block, so an
# error never closes the person's PowerShell window.
# LEADIN_HELPER_BASE_URL replaces the release URL, for tests.
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    $Version = '__VERSION__'
    $BaseUrl = if ($env:LEADIN_HELPER_BASE_URL) { $env:LEADIN_HELPER_BASE_URL } else { "https://github.com/G-DPP/leadin-helper/releases/download/v$Version" }

    if (-not [Environment]::Is64BitOperatingSystem) {
        Write-Error 'The LeadIn helper needs 64-bit Windows.'
        return
    }
    $File = "leadin-helper_${Version}_windows_amd64.exe"
    $Temp = Join-Path ([IO.Path]::GetTempPath()) ('leadin-helper-' + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $Temp | Out-Null
    try {
        Write-Host "Downloading LeadIn helper $Version for windows/amd64..."
        Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/SHA256SUMS" -OutFile (Join-Path $Temp 'SHA256SUMS')
        Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$File" -OutFile (Join-Path $Temp $File)

        $Expected = $null
        foreach ($Line in Get-Content (Join-Path $Temp 'SHA256SUMS')) {
            $Parts = $Line -split '\s+', 2
            if ($Parts.Count -eq 2 -and ($Parts[1] -eq $File -or $Parts[1] -eq "*$File")) { $Expected = $Parts[0] }
        }
        if (-not $Expected) { throw "$File is not in SHA256SUMS" }
        $Actual = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $Temp $File)).Hash.ToLowerInvariant()
        if ($Expected -ne $Actual) { throw "the hash of $File does not match SHA256SUMS: nothing was installed" }
        Write-Host "Hash checked: $Actual"

        & (Join-Path $Temp $File) --install
        if ($LASTEXITCODE -ne 0) { throw "the installation failed (exit code $LASTEXITCODE)" }
    }
    catch {
        Write-Host "ERROR: $($_.Exception.Message)" -ForegroundColor Red
    }
    finally {
        Remove-Item -Recurse -Force -Path $Temp -ErrorAction SilentlyContinue
    }
}
