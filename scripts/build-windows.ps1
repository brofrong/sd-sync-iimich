# Builds the Windows release on Windows: the frontend, sd-sync-immich.exe
# (x64, with its icon, manifest and version info) and the installer
# SD-Sync-Immich-Setup-x64.exe (build/windows/installer.nsi), into OutDir:
#
#   sd-sync-immich.exe             the app (not uploaded; inside the installer)
#   SD-Sync-Immich-Setup-x64.exe   the installer: manual installs and the
#                                  in-app updater download this asset
#
# Usage: scripts/build-windows.ps1 -Version <version> -OutDir <dir> [-SkipFrontend]
#
# The version (e.g. v0.5.0, or dev) is embedded with -ldflags; the
# installer's version is its MAJOR.MINOR.PATCH (0.0.0 for others). Needs Go,
# Node.js and NSIS 3 (makensis on PATH or in its default folder). The Wails
# CLI is pinned to the Wails version in go.mod and run through `go run`.
# No cgo: Wails on Windows needs none, so no C compiler either.
[CmdletBinding()]
param(
	[Parameter(Mandatory = $true)][string]$Version,
	[Parameter(Mandatory = $true)][string]$OutDir,
	# Reuse frontend/dist (e.g. built by scripts/build-frontend.sh).
	[switch]$SkipFrontend
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$root = Split-Path -Parent $PSScriptRoot
$wails3 = 'github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.34'
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$out = (Resolve-Path $OutDir).Path
$exe = Join-Path $out 'sd-sync-immich.exe'
$setup = Join-Path $out 'SD-Sync-Immich-Setup-x64.exe'

# Runs a native command and stops on a non-zero exit code, which
# $ErrorActionPreference does not do for native commands.
function Invoke-Native {
	param([Parameter(Mandatory = $true)][string]$Command, [string[]]$Arguments = @())
	& $Command @Arguments
	if ($LASTEXITCODE -ne 0) {
		throw "$Command $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
	}
}

# makensis on PATH, or where the NSIS installer (and Chocolatey) puts it.
function Find-Makensis {
	$cmd = Get-Command makensis -ErrorAction SilentlyContinue
	if ($cmd) { return $cmd.Source }
	foreach ($dir in @(${env:ProgramFiles(x86)}, $env:ProgramFiles)) {
		if (-not $dir) { continue }
		$path = Join-Path $dir 'NSIS\makensis.exe'
		if (Test-Path $path) { return $path }
	}
	throw 'makensis (NSIS 3) not found: install NSIS, e.g. choco install nsis'
}

$makensis = Find-Makensis
Push-Location $root
try {
	# The same steps as scripts/build-frontend.sh: npm dependencies from the
	# lockfile, TypeScript bindings for the Go services, the Vite build.
	if (-not $SkipFrontend) {
		Push-Location frontend
		try { Invoke-Native npm @('ci', '--no-audit', '--no-fund') } finally { Pop-Location }
		Invoke-Native go @('run', $wails3, 'generate', 'bindings', '-silent', '-clean=true', '-d', 'frontend/bindings', '-ts', './cmd/sd-sync-immich')
		Push-Location frontend
		try { Invoke-Native npm @('run', 'build') } finally { Pop-Location }
	}

	$numeric = '0.0.0'
	if ($Version -match '^v?(\d+\.\d+\.\d+)$') { $numeric = $Matches[1] }

	# Icon, manifest and version info go into the executable through a
	# .syso object next to main.go, which `go build` links in. The
	# version info is written here, as it carries the version.
	$work = Join-Path ([System.IO.Path]::GetTempPath()) ("sd-sync-immich-build-" + [guid]::NewGuid())
	New-Item -ItemType Directory -Path $work | Out-Null
	$syso = Join-Path $root 'cmd/sd-sync-immich/rsrc_windows_amd64.syso'
	try {
		$info = [ordered]@{
			fixed = [ordered]@{ file_version = "$numeric.0"; product_version = "$numeric.0" }
			info  = [ordered]@{
				'0409' = [ordered]@{
					ProductName     = 'SD Sync Immich'
					FileDescription = 'SD Sync Immich'
					ProductVersion  = $Version
					FileVersion     = $Version
					LegalCopyright  = 'MIT License'
					OriginalFilename = 'sd-sync-immich.exe'
				}
			}
		}
		$infoPath = Join-Path $work 'info.json'
		# UTF-8 without a BOM: winres reads plain JSON.
		[System.IO.File]::WriteAllText($infoPath, ($info | ConvertTo-Json -Depth 5), (New-Object System.Text.UTF8Encoding $false))
		Invoke-Native go @('run', $wails3, 'generate', 'syso', '-arch', 'amd64',
			'-icon', 'build/windows/icon.ico',
			'-manifest', 'build/windows/sd-sync-immich.exe.manifest',
			'-info', $infoPath,
			'-out', $syso)

		# -tags production: without it Wails runs in development mode (web
		# inspector, dev-server asset handling). -H windowsgui: no console
		# window.
		$ldflags = "-s -w -H windowsgui -X github.com/dmitiigorshkov/sd-sync-immich/internal/version.Current=$Version"
		$env:GOOS = 'windows'
		$env:GOARCH = 'amd64'
		$env:CGO_ENABLED = '0'
		Invoke-Native go @('build', '-tags', 'production', '-ldflags', $ldflags, '-o', $exe, './cmd/sd-sync-immich')
	}
	finally {
		Remove-Item -Force -ErrorAction SilentlyContinue $syso
		Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $work
	}

	Invoke-Native $makensis @('-WX', '-INPUTCHARSET', 'UTF8', "-DVERSION=$numeric", "-DAPP_EXE=$exe", "-DOUTFILE=$setup", (Join-Path $root 'build/windows/installer.nsi'))
}
finally {
	Pop-Location
}

Write-Host "Built $Version into $out"
