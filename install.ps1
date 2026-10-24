# Installs the stackpilot binary from GitHub Releases on Windows.
#
#   irm https://raw.githubusercontent.com/qureshikaif/stack-pilot/main/install.ps1 | iex
#
# Environment variables:
#   STACKPILOT_VERSION      release tag to install, e.g. v0.1.0 (default: latest)
#   STACKPILOT_INSTALL_DIR  where to put stackpilot.exe
#                           (default: %LOCALAPPDATA%\Programs\stackpilot)
#   STACKPILOT_BASE_URL     download from a mirror instead of GitHub
#
# Works in Windows PowerShell 5.1 and PowerShell 7+.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # makes Invoke-WebRequest much faster in 5.1
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

& {
    $repo = 'qureshikaif/stack-pilot'
    $version = if ($env:STACKPILOT_VERSION) { $env:STACKPILOT_VERSION } else { 'latest' }
    $installDir = if ($env:STACKPILOT_INSTALL_DIR) { $env:STACKPILOT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\stackpilot' }

    $arch = switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "stackpilot install: unsupported CPU $($env:PROCESSOR_ARCHITECTURE)" }
    }

    $base = if ($env:STACKPILOT_BASE_URL) { $env:STACKPILOT_BASE_URL }
            elseif ($version -eq 'latest') { "https://github.com/$repo/releases/latest/download" }
            else { "https://github.com/$repo/releases/download/$version" }
    $archive = "stackpilot_windows_$arch.zip"

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("stackpilot-" + [Guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "Downloading $archive ($version)..."
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$archive" -OutFile (Join-Path $tmp $archive)
        Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

        $want = Get-Content (Join-Path $tmp 'checksums.txt') |
            ForEach-Object { $f = $_ -split '\s+'; if ($f[1] -eq $archive) { $f[0] } } |
            Select-Object -First 1
        if (-not $want) { throw "stackpilot install: $archive is not listed in checksums.txt" }
        $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $archive)).Hash.ToLower()
        if ($got -ne $want.ToLower()) { throw "stackpilot install: checksum mismatch for $archive (got $got, want $want)" }
        Write-Host 'Checksum verified.'

        Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x') -Force
        New-Item -ItemType Directory -Force -Path $installDir | Out-Null
        $exe = Join-Path $installDir 'stackpilot.exe'
        Copy-Item -Force (Join-Path $tmp 'x\stackpilot.exe') $exe
    }
    finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }

    # Add to the user PATH in the registry, keeping its type (REG_EXPAND_SZ)
    # so existing %VARIABLE% entries stay unexpanded.
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    try {
        $userPath = [string]$key.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
        $kind = if ($userPath) { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
        $entries = @($userPath -split ';' | Where-Object { $_ })
        $present = $entries | Where-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') -ieq $installDir.TrimEnd('\') }
        if (-not $present) {
            $key.SetValue('Path', (($entries + $installDir) -join ';'), $kind)
            # Tell Explorer and new terminals that the environment changed.
            if (-not ('StackPilot.Env' -as [type])) {
                Add-Type -Namespace StackPilot -Name Env -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
            }
            $result = [UIntPtr]::Zero
            [void][StackPilot.Env]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
            Write-Host "Added $installDir to your user PATH."
        }
    }
    finally {
        $key.Close()
    }
    if (-not (($env:Path -split ';') -contains $installDir)) { $env:Path = "$env:Path;$installDir" }

    Write-Host "Installed $(& $exe version) to $exe"
    Write-Host 'Run: stackpilot setup   (open a new terminal first if this one does not find it)'
}
